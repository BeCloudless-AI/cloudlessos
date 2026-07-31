#!/usr/bin/env python3
"""Verify a downloaded beta generation before stable promotion."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import pathlib
import re


PACKAGES = {
    "cloudless-orchestrator",
    "cloudless-shell",
    "cloudless-branding",
    "cloudless-hardware",
    "cloudless-firstboot",
    "cloudless-updater",
}
ARCHITECTURES = {"amd64", "arm64"}
ARTIFACTS = {
    "install-dgx-spark.sh",
    "cloudless-apps-manifest.json",
    "cloudless-models.json",
    "cloudless-diffusion.json",
    "cloudless-trust-inventory.json",
    "cloudless-physical-qualification.json",
}
REQUIRED_GATES = {
    "go-tests", "go-vet", "web-javascript", "app-manifest-v2", "backup-recovery",
    "installer-preflight", "platform-matrix", "package-architecture", "package-contents",
    "package-lifecycle", "release-isolation", "release-preflight", "secret-hygiene",
    "service-hardening", "sbom", "trust-inventory", "vulnerability-scan",
    "updater-workload-continuity", "atomic-repository",
}
REQUIRED_TARGETS = {
    ("generic", "amd64"),
    ("generic", "arm64"),
    ("dgx-spark", "arm64"),
}


def digest(path: pathlib.Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def checked_file(root: pathlib.Path, name: str, expected_hash: str, expected_size: int) -> pathlib.Path:
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._+~-]*", name):
        raise ValueError(f"unsafe promotion filename: {name}")
    path = root / name
    if not path.is_file() or path.stat().st_size != expected_size or digest(path) != expected_hash:
        raise ValueError(f"promotion payload integrity mismatch: {name}")
    return path


def parse_time(value: object) -> dt.datetime:
    if not isinstance(value, str):
        raise ValueError("beta manifest has no publication time")
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("beta publication time has no timezone")
    return parsed.astimezone(dt.timezone.utc)


def verify(args: argparse.Namespace) -> dict:
    manifest_path = pathlib.Path(args.manifest)
    package_dir = pathlib.Path(args.packages)
    artifact_dir = pathlib.Path(args.artifacts)
    release = json.loads(manifest_path.read_text(encoding="utf-8"))
    if release.get("schema") != "cloudless.release.v2" or release.get("channel") != "beta":
        raise ValueError("promotion source is not a signed CloudlessOS beta release")
    if release.get("version") != args.version or release.get("sourceCommit") != args.source_commit:
        raise ValueError("beta version or source commit does not match the stable candidate")
    validation = release.get("validation")
    if not isinstance(validation, dict) or validation.get("schema") != "cloudless.release-gates.v1":
        raise ValueError("beta release has no valid gate attestation")
    for field, expected in (("version", args.version), ("channel", "beta"), ("sourceCommit", args.source_commit)):
        if validation.get(field) != expected:
            raise ValueError(f"beta gate attestation {field} mismatch")
    if set(validation.get("passedGates", [])) != REQUIRED_GATES:
        raise ValueError("beta release did not pass the complete production gate set")
    targets = {(item.get("platform"), item.get("architecture")) for item in validation.get("targets", [])}
    if targets != REQUIRED_TARGETS:
        raise ValueError("beta release platform matrix is incomplete")
    compatibility = release.get("compatibility")
    if (not isinstance(compatibility, dict) or
            compatibility.get("matrixSha256") != validation.get("matrixSha256") or
            compatibility.get("targets") != validation.get("targets")):
        raise ValueError("beta compatibility contract is not bound to its gate matrix")
    physical = release.get("physicalQualification")
    if not isinstance(physical, dict) or physical != validation.get("physicalQualification"):
        raise ValueError("beta physical qualification is not bound to its gate attestation")
    if physical.get("version") != args.version or physical.get("channel") != "beta" or physical.get("sourceCommit") != args.source_commit:
        raise ValueError("beta physical qualification identity mismatch")
    if physical.get("required") is not False or physical.get("status") not in {"qualified", "not-qualified"}:
        raise ValueError("beta physical qualification policy is invalid")
    published = parse_time(release.get("publishedAt"))
    now = parse_time(args.now) if args.now else dt.datetime.now(dt.timezone.utc)
    age = int((now - published).total_seconds())
    if age < args.minimum_age_seconds:
        remaining = args.minimum_age_seconds - age
        raise ValueError(f"beta soak is incomplete: {remaining} seconds remain")

    package_records = release.get("packages")
    if not isinstance(package_records, list):
        raise ValueError("beta package inventory is missing")
    identities: set[tuple[str, str]] = set()
    verified_packages = []
    for item in package_records:
        identity = (item.get("name"), item.get("architecture"))
        if identity[0] not in PACKAGES or identity[1] not in ARCHITECTURES or identity in identities:
            raise ValueError(f"invalid or duplicate beta package identity: {identity}")
        identities.add(identity)
        filename = pathlib.PurePosixPath(str(item.get("filename", ""))).name
        checked_file(package_dir, filename, str(item.get("sha256", "")), int(item.get("size", -1)))
        verified_packages.append({
            "name": identity[0],
            "architecture": identity[1],
            "version": item.get("version"),
            "filename": filename,
            "sha256": item.get("sha256"),
            "size": item.get("size"),
        })
    expected_identities = {(name, architecture) for name in PACKAGES for architecture in ARCHITECTURES}
    if identities != expected_identities:
        raise ValueError("beta package inventory is not the complete six-by-two generation")

    artifact_records = release.get("artifacts")
    if not isinstance(artifact_records, list):
        raise ValueError("beta artifact inventory is missing")
    expected_artifacts = set(ARTIFACTS) | {f"cloudless-{args.version}.spdx.json"}
    names: set[str] = set()
    verified_artifacts = []
    prefix = f"artifacts/{args.version}/beta/"
    for item in artifact_records:
        name = item.get("name")
        if name not in expected_artifacts or name in names:
            raise ValueError(f"invalid or duplicate beta artifact: {name}")
        names.add(name)
        if item.get("path") != prefix + name or item.get("signature") != prefix + name + ".asc":
            raise ValueError(f"beta artifact is not channel scoped: {name}")
        checked_file(artifact_dir, name, str(item.get("sha256", "")), int(item.get("size", -1)))
        checked_file(
            artifact_dir,
            name + ".asc",
            str(item.get("signatureSha256", "")),
            int(item.get("signatureSize", -1)),
        )
        verified_artifacts.append({"name": name, "sha256": item.get("sha256"), "size": item.get("size")})
    if names != expected_artifacts:
        raise ValueError("beta standalone artifact inventory is incomplete")

    return {
        "schema": "cloudless.beta-promotion.v1",
        "version": args.version,
        "sourceCommit": args.source_commit,
        "betaManifestSha256": digest(manifest_path),
        "betaPublishedAt": release["publishedAt"],
        "soakAgeSeconds": age,
        "packages": sorted(verified_packages, key=lambda item: (item["architecture"], item["name"])),
        "artifacts": sorted(verified_artifacts, key=lambda item: item["name"]),
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("manifest")
    parser.add_argument("packages")
    parser.add_argument("artifacts")
    parser.add_argument("version")
    parser.add_argument("source_commit")
    parser.add_argument("--minimum-age-seconds", type=int, default=7 * 24 * 60 * 60)
    parser.add_argument("--now", default="")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    try:
        result = verify(args)
    except (OSError, ValueError, TypeError, json.JSONDecodeError) as exc:
        raise SystemExit(f"Beta promotion rejected: {exc}") from exc
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
