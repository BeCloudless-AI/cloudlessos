#!/usr/bin/python3
"""Bind retained physical qualification evidence to one CloudlessOS release."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import importlib.machinery
import importlib.util
import json
import os
import re
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
QUALIFIER = ROOT / "distro/packages/cloudless-firstboot/cloudless-qualify"
DEFAULT_MATRIX = ROOT / "distro/release/physical-validation-matrix.json"
VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(?:[-+~][0-9A-Za-z][0-9A-Za-z.+~_-]*)?$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
SCHEMA = "cloudless.physical-release.v1"


def load_qualifier():
    loader = importlib.machinery.SourceFileLoader("cloudless_release_qualifier", str(QUALIFIER))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def physical_evidence_required(version: str, channel: str) -> bool:
    return channel == "stable" and int(version.split(".", 1)[0]) >= 1


def prepare(version: str, channel: str, commit: str, matrix: Path, archives: list[Path]) -> dict:
    if not VERSION_RE.fullmatch(version):
        raise ValueError("version must be a semantic CloudlessOS version")
    if channel not in {"stable", "beta"}:
        raise ValueError("channel must be stable or beta")
    if not COMMIT_RE.fullmatch(commit):
        raise ValueError("source commit must contain 40 lowercase hexadecimal characters")
    required = physical_evidence_required(version, channel)
    matrix_hash = sha256_file(matrix)
    if not archives:
        if required:
            raise ValueError("CloudlessOS 1.0+ stable releases require the complete physical qualification set")
        return {
            "schema": SCHEMA,
            "status": "not-qualified",
            "required": False,
            "version": version,
            "channel": channel,
            "sourceCommit": commit,
            "matrixSha256": matrix_hash,
            "preparedAt": utc_now(),
            "reason": "A complete physical qualification set was not supplied for this pre-1.0 release.",
        }
    qualifier = load_qualifier()
    qualification_set = qualifier.verify_export_set(archives, matrix)
    if qualification_set.get("version") != version or qualification_set.get("sourceCommit") != commit:
        raise ValueError("physical qualification set does not match the release version and source commit")
    if qualification_set.get("matrixSha256") != matrix_hash:
        raise ValueError("physical qualification set does not match the authoritative matrix")
    canonical = json.dumps(qualification_set, sort_keys=True, separators=(",", ":")).encode()
    return {
        "schema": SCHEMA,
        "status": "qualified",
        "required": required,
        "version": version,
        "channel": channel,
        "sourceCommit": commit,
        "matrixSha256": matrix_hash,
        "preparedAt": utc_now(),
        "qualificationSetSha256": hashlib.sha256(canonical).hexdigest(),
        "qualificationSet": qualification_set,
    }


def atomic_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(payload, handle, indent=2, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary_name, 0o600)
        os.replace(temporary_name, path)
    finally:
        try:
            os.unlink(temporary_name)
        except FileNotFoundError:
            pass


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version")
    parser.add_argument("channel", choices=("stable", "beta"))
    parser.add_argument("commit")
    parser.add_argument("--matrix", type=Path, default=DEFAULT_MATRIX)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("archives", nargs="*", type=Path)
    args = parser.parse_args()
    try:
        payload = prepare(args.version, args.channel, args.commit, args.matrix, args.archives)
        atomic_json(args.output, payload)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: {exc}", file=os.sys.stderr)
        return 1
    print(f"Physical qualification: {payload['status']} ({args.output})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
