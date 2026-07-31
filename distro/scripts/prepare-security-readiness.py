#!/usr/bin/python3
"""Create the public security-operations descriptor for one release."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import tempfile
from pathlib import Path
from urllib.parse import urlparse


SCHEMA = "cloudless.security-readiness.v1"
ATTESTATION_SCHEMA = "cloudless.security-contact-attestation.v1"
VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(?:[-+~][0-9A-Za-z][0-9A-Za-z.+~_-]*)?$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
MAX_ATTESTATION_AGE = dt.timedelta(days=30)
FUTURE_TOLERANCE = dt.timedelta(minutes=5)


def utc_now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def parse_time(value: object) -> dt.datetime:
    if not isinstance(value, str) or not value.strip():
        raise ValueError("verifiedAt must be an ISO-8601 timestamp")
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError("verifiedAt must be an ISO-8601 timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError("verifiedAt must include a timezone")
    return parsed.astimezone(dt.timezone.utc)


def valid_contact(value: object) -> bool:
    if not isinstance(value, str) or len(value) > 254:
        return False
    parsed = urlparse(value)
    if parsed.scheme == "mailto":
        address = parsed.path
        return "@" in address and not parsed.query and not parsed.fragment
    return parsed.scheme == "https" and bool(parsed.netloc) and not parsed.username and not parsed.password


def required_for(version: str, channel: str) -> bool:
    return channel == "stable" and int(version.split(".", 1)[0]) >= 1


def prepare(version: str, channel: str, commit: str, attestation: Path | None, now: dt.datetime | None = None) -> dict:
    if not VERSION_RE.fullmatch(version):
        raise ValueError("version must be a semantic CloudlessOS version")
    if channel not in {"stable", "beta"}:
        raise ValueError("channel must be stable or beta")
    if not COMMIT_RE.fullmatch(commit):
        raise ValueError("source commit must contain 40 lowercase hexadecimal characters")
    required = required_for(version, channel)
    current = now or utc_now()
    base = {
        "schema": SCHEMA,
        "version": version,
        "channel": channel,
        "sourceCommit": commit,
        "required": required,
        "preparedAt": iso(current),
    }
    if attestation is None:
        if required:
            raise ValueError("CloudlessOS 1.0+ stable releases require a current security-contact attestation")
        return {
            **base,
            "status": "not-operational",
            "reason": "A monitored security contact and escalation owner were not attested for this pre-1.0 release.",
        }

    document = json.loads(attestation.read_text(encoding="utf-8"))
    if not isinstance(document, dict) or document.get("schema") != ATTESTATION_SCHEMA:
        raise ValueError("invalid security-contact attestation schema")
    allowed = {
        "schema", "monitored", "securityContact", "escalationOwner",
        "verifiedAt", "acknowledgementBusinessDays",
    }
    unknown = set(document) - allowed
    if unknown:
        raise ValueError(f"unknown security-contact attestation fields: {', '.join(sorted(unknown))}")
    if document.get("monitored") is not True:
        raise ValueError("security contact must be explicitly attested as monitored")
    contact = document.get("securityContact")
    if not valid_contact(contact):
        raise ValueError("securityContact must be a mailto: or HTTPS URL without credentials")
    owner = document.get("escalationOwner")
    if not isinstance(owner, str) or not owner.strip() or len(owner.strip()) > 120:
        raise ValueError("escalationOwner must name a non-empty operational role")
    target = document.get("acknowledgementBusinessDays")
    if not isinstance(target, int) or isinstance(target, bool) or not 1 <= target <= 3:
        raise ValueError("acknowledgementBusinessDays must be between 1 and 3")
    verified = parse_time(document.get("verifiedAt"))
    if verified > current + FUTURE_TOLERANCE:
        raise ValueError("security-contact attestation is dated in the future")
    if current - verified > MAX_ATTESTATION_AGE:
        raise ValueError("security-contact attestation is older than 30 days")
    return {
        **base,
        "status": "operational",
        "monitored": True,
        "securityContact": contact,
        "escalationOwner": owner.strip(),
        "verifiedAt": iso(verified),
        "acknowledgementBusinessDays": target,
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
    parser.add_argument("--attestation", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        payload = prepare(args.version, args.channel, args.commit, args.attestation)
        atomic_json(args.output, payload)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: {exc}", file=os.sys.stderr)
        return 1
    print(f"Security readiness: {payload['status']} ({args.output})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
