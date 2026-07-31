#!/usr/bin/python3
"""Bind an exact-commit GitHub qualification run to one CloudlessOS release."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import subprocess
import tempfile
from pathlib import Path


SCHEMA = "cloudless.ci-qualification.v1"
VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(?:[-+~][0-9A-Za-z][0-9A-Za-z.+~_-]*)?$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
WORKFLOW_PATH = ".github/workflows/multiarch.yml"
WORKFLOW_NAME = "CloudlessOS qualification"
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


def gh_json(repository: str, endpoint: str, fields: dict[str, str] | None = None) -> dict:
    command = ["gh", "api", "--method", "GET", f"repos/{repository}/{endpoint}"]
    for key, value in (fields or {}).items():
        command.extend(("-f", f"{key}={value}"))
    try:
        result = subprocess.run(command, check=True, capture_output=True, text=True)
    except FileNotFoundError as exc:
        raise ValueError("GitHub CLI is required to collect CI qualification evidence") from exc
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or "").strip()
        raise ValueError(f"GitHub CI evidence query failed: {detail or 'unknown gh error'}") from exc
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise ValueError("GitHub CI evidence query returned invalid JSON") from exc
    if not isinstance(payload, dict):
        raise ValueError("GitHub CI evidence query returned an invalid document")
    return payload


def live_evidence(repository: str, commit: str) -> tuple[dict, dict, dict]:
    if not REPOSITORY_RE.fullmatch(repository):
        raise ValueError("repository must be in owner/name form")
    runs = gh_json(repository, "actions/workflows/multiarch.yml/runs", {
        "head_sha": commit, "status": "success", "per_page": "100",
    })
    candidates = [
        run for run in runs.get("workflow_runs", [])
        if isinstance(run, dict) and run.get("head_sha") == commit
        and run.get("path") == WORKFLOW_PATH and run.get("name") == WORKFLOW_NAME
        and run.get("status") == "completed" and run.get("conclusion") == "success"
    ]
    if not candidates:
        raise ValueError("no successful CloudlessOS qualification run exists for the exact source commit")
    run = max(candidates, key=lambda item: int(item.get("id", 0)))
    run_id = int(run.get("id", 0))
    if run_id <= 0:
        raise ValueError("GitHub qualification run has no valid identity")
    jobs = gh_json(repository, f"actions/runs/{run_id}/jobs", {"per_page": "100"})
    artifacts = gh_json(repository, f"actions/runs/{run_id}/artifacts", {"per_page": "100"})
    return {"workflow_runs": [run]}, jobs, artifacts


def load_fixture(directory: Path) -> tuple[dict, dict, dict]:
    documents = []
    for name in ("runs.json", "jobs.json", "artifacts.json"):
        path = directory / name
        documents.append(json.loads(path.read_text(encoding="utf-8")))
    if not all(isinstance(item, dict) for item in documents):
        raise ValueError("CI qualification fixture documents must be JSON objects")
    return tuple(documents)  # type: ignore[return-value]


def verify(version: str, channel: str, commit: str, repository: str, evidence: tuple[dict, dict, dict]) -> dict:
    runs, jobs_document, artifacts_document = evidence
    matching = [
        run for run in runs.get("workflow_runs", [])
        if isinstance(run, dict) and run.get("head_sha") == commit
        and run.get("path") == WORKFLOW_PATH and run.get("name") == WORKFLOW_NAME
        and run.get("status") == "completed" and run.get("conclusion") == "success"
    ]
    if len(matching) != 1:
        raise ValueError("CI evidence must contain exactly one successful exact-commit qualification run")
    run = matching[0]
    run_id = run.get("id")
    attempt = run.get("run_attempt")
    if not isinstance(run_id, int) or run_id <= 0 or not isinstance(attempt, int) or attempt <= 0:
        raise ValueError("CI qualification run identity is invalid")
    if run.get("event") not in {"push", "workflow_dispatch"}:
        raise ValueError("CI qualification run used an unsupported event")
    jobs = jobs_document.get("jobs")
    if not isinstance(jobs, list):
        raise ValueError("CI qualification jobs are missing")
    names: set[str] = set()
    for job in jobs:
        if not isinstance(job, dict) or not isinstance(job.get("name"), str):
            raise ValueError("CI qualification contains an invalid job record")
        name = job["name"]
        if name in names:
            raise ValueError(f"CI qualification contains duplicate job {name}")
        names.add(name)
        if name in REQUIRED_JOBS and (job.get("status") != "completed" or job.get("conclusion") != "success"):
            raise ValueError(f"required CI job did not succeed: {name}")
    if not REQUIRED_JOBS.issubset(names):
        missing = ", ".join(sorted(REQUIRED_JOBS - names))
        raise ValueError(f"CI qualification is missing required jobs: {missing}")

    artifacts = artifacts_document.get("artifacts")
    if not isinstance(artifacts, list):
        raise ValueError("CI qualification artifacts are missing")
    artifact_names: set[str] = set()
    expected = {f"{prefix}-{run_id}-{attempt}" for prefix in ARTIFACT_PREFIXES}
    for artifact in artifacts:
        if not isinstance(artifact, dict) or not isinstance(artifact.get("name"), str):
            raise ValueError("CI qualification contains an invalid artifact record")
        if artifact.get("expired") is True:
            continue
        artifact_names.add(artifact["name"])
    if not expected.issubset(artifact_names):
        missing = ", ".join(sorted(expected - artifact_names))
        raise ValueError(f"CI qualification is missing retained evidence: {missing}")

    return {
        "schema": SCHEMA,
        "status": "qualified",
        "required": required_for(version, channel),
        "version": version,
        "channel": channel,
        "sourceCommit": commit,
        "preparedAt": utc_now(),
        "repository": repository,
        "workflow": WORKFLOW_PATH,
        "runId": run_id,
        "runAttempt": attempt,
        "runUrl": run.get("html_url"),
        "event": run.get("event"),
        "completedAt": run.get("updated_at"),
        "jobs": sorted(REQUIRED_JOBS),
        "artifacts": sorted(expected),
    }


def prepare(version: str, channel: str, commit: str, repository: str, fixture: Path | None) -> dict:
    if not VERSION_RE.fullmatch(version):
        raise ValueError("version must be a semantic CloudlessOS version")
    if channel not in {"stable", "beta"}:
        raise ValueError("channel must be stable or beta")
    if not COMMIT_RE.fullmatch(commit):
        raise ValueError("source commit must contain 40 lowercase hexadecimal characters")
    required = required_for(version, channel)
    if fixture is None and not required:
        return {
            "schema": SCHEMA,
            "status": "not-qualified",
            "required": False,
            "version": version,
            "channel": channel,
            "sourceCommit": commit,
            "preparedAt": utc_now(),
            "reason": "A successful exact-commit CI evidence set was not supplied for this pre-1.0 release.",
        }
    if fixture is not None:
        evidence = load_fixture(fixture)
    else:
        if not repository:
            raise ValueError("CloudlessOS 1.0+ stable releases require a GitHub repository identity")
        evidence = live_evidence(repository, commit)
    return verify(version, channel, commit, repository, evidence)


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
    parser.add_argument("--repository", default="")
    parser.add_argument("--fixture-dir", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        payload = prepare(args.version, args.channel, args.commit, args.repository, args.fixture_dir)
        atomic_json(args.output, payload)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: {exc}", file=os.sys.stderr)
        return 1
    print(f"CI qualification: {payload['status']} ({args.output})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
