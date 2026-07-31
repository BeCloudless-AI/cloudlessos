#!/usr/bin/python3
"""Bind a retained local exact-commit qualification run to a CloudlessOS release."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import tempfile
from pathlib import Path


SCHEMA = "cloudless.ci-qualification.v1"
EVIDENCE_SCHEMA = "cloudless.local-ci-evidence.v1"
LOCAL_RUNNER = "distro/scripts/run-local-qualification.sh"
VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(?:[-+~][0-9A-Za-z][0-9A-Za-z.+~_-]*)?$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
REQUIRED_JOBS = {
    "Source, concurrency and security policies",
    "generic / amd64",
    "generic / arm64",
    "dgx-spark / arm64",
    "AMD64 and ARM64 package payloads",
    "Interface capture smoke test",
    "Durable lifecycle soak",
}
ARTIFACT_PREFIXES = {"package-qualification", "visual-regression", "lifecycle-soak"}


def required_for(version: str, channel: str) -> bool:
    return channel == "stable" and int(version.split(".", 1)[0]) >= 1


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _read_regular_file(root: Path, relative: str, expected_sha256: str, expected_size: int) -> dict:
    if not relative or Path(relative).name != relative:
        raise ValueError("local qualification evidence paths must be plain file names")
    if not SHA256_RE.fullmatch(expected_sha256):
        raise ValueError(f"local qualification evidence has an invalid SHA-256 for {relative}")
    if not isinstance(expected_size, int) or isinstance(expected_size, bool) or expected_size <= 0:
        raise ValueError(f"local qualification evidence has an invalid size for {relative}")
    path = root / relative
    try:
        status = path.lstat()
    except FileNotFoundError as exc:
        raise ValueError(f"local qualification evidence is missing {relative}") from exc
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"local qualification evidence is not a regular file: {relative}")
    if status.st_size != expected_size:
        raise ValueError(f"local qualification evidence size changed: {relative}")
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    if digest != expected_sha256:
        raise ValueError(f"local qualification evidence digest changed: {relative}")
    return {"file": relative, "sha256": digest, "size": expected_size}


def load_local_evidence(path: Path, commit: str) -> dict:
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"local qualification evidence is unreadable: {exc}") from exc
    if not isinstance(document, dict) or document.get("schema") != EVIDENCE_SCHEMA:
        raise ValueError("local qualification evidence schema is invalid")
    if document.get("sourceCommit") != commit:
        raise ValueError("local qualification evidence does not match the exact source commit")
    run_id = document.get("runId")
    attempt = document.get("runAttempt")
    if not isinstance(run_id, int) or isinstance(run_id, bool) or run_id <= 0:
        raise ValueError("local qualification run identity is invalid")
    if not isinstance(attempt, int) or isinstance(attempt, bool) or attempt <= 0:
        raise ValueError("local qualification run attempt is invalid")

    root = path.parent
    jobs = document.get("jobs")
    if not isinstance(jobs, list):
        raise ValueError("local qualification jobs are missing")
    job_evidence = []
    names: set[str] = set()
    for job in jobs:
        if not isinstance(job, dict) or not isinstance(job.get("name"), str):
            raise ValueError("local qualification contains an invalid job record")
        name = job["name"]
        if name in names or name not in REQUIRED_JOBS:
            raise ValueError(f"local qualification contains an unexpected or duplicate job: {name}")
        names.add(name)
        if job.get("status") != "success":
            raise ValueError(f"required local qualification job did not succeed: {name}")
        retained = _read_regular_file(root, job.get("log", ""), job.get("sha256", ""), job.get("size", 0))
        job_evidence.append({"name": name, **retained})
    if names != REQUIRED_JOBS:
        raise ValueError("local qualification job inventory is incomplete")

    artifacts = document.get("artifacts")
    if not isinstance(artifacts, list):
        raise ValueError("local qualification artifacts are missing")
    expected_names = {f"{prefix}-{run_id}-{attempt}" for prefix in ARTIFACT_PREFIXES}
    artifact_evidence = []
    artifact_names: set[str] = set()
    for artifact in artifacts:
        if not isinstance(artifact, dict) or not isinstance(artifact.get("name"), str):
            raise ValueError("local qualification contains an invalid artifact record")
        name = artifact["name"]
        if name in artifact_names or name not in expected_names:
            raise ValueError(f"local qualification contains an unexpected or duplicate artifact: {name}")
        artifact_names.add(name)
        retained = _read_regular_file(
            root, artifact.get("file", ""), artifact.get("sha256", ""), artifact.get("size", 0)
        )
        artifact_evidence.append({"name": name, **retained})
    if artifact_names != expected_names:
        raise ValueError("local qualification evidence inventory is incomplete")

    return {
        "runId": run_id,
        "runAttempt": attempt,
        "startedAt": document.get("startedAt"),
        "completedAt": document.get("completedAt"),
        "jobs": sorted(names),
        "artifacts": sorted(artifact_names),
        "jobEvidence": sorted(job_evidence, key=lambda item: item["name"]),
        "artifactEvidence": sorted(artifact_evidence, key=lambda item: item["name"]),
    }


def prepare(version: str, channel: str, commit: str, evidence_path: Path | None) -> dict:
    if not VERSION_RE.fullmatch(version):
        raise ValueError("version must be a semantic CloudlessOS version")
    if channel not in {"stable", "beta"}:
        raise ValueError("channel must be stable or beta")
    if not COMMIT_RE.fullmatch(commit):
        raise ValueError("source commit must contain 40 lowercase hexadecimal characters")
    required = required_for(version, channel)
    if evidence_path is None:
        if required:
            raise ValueError("CloudlessOS 1.0+ stable releases require retained local qualification evidence")
        return {
            "schema": SCHEMA,
            "status": "not-qualified",
            "required": False,
            "version": version,
            "channel": channel,
            "sourceCommit": commit,
            "preparedAt": utc_now(),
            "reason": "A complete local exact-commit evidence set was not supplied for this pre-1.0 release.",
        }

    evidence = load_local_evidence(evidence_path, commit)
    return {
        "schema": SCHEMA,
        "status": "qualified",
        "required": required,
        "version": version,
        "channel": channel,
        "sourceCommit": commit,
        "preparedAt": utc_now(),
        "runner": "cloudless-local-release",
        "workflow": LOCAL_RUNNER,
        **evidence,
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
    parser.add_argument("--local-evidence", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        payload = prepare(args.version, args.channel, args.commit, args.local_evidence)
        atomic_json(args.output, payload)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: {exc}", file=os.sys.stderr)
        return 1
    print(f"Local CI qualification: {payload['status']} ({args.output})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
