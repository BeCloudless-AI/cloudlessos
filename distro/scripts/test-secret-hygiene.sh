#!/usr/bin/env bash
# Reject credential material from every non-ignored source file without ever
# printing the matched value.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

python3 - <<'PY'
import pathlib
import re
import subprocess

rules = {
    "Cloudflare API token": re.compile(rb"cfat_[A-Za-z0-9_-]{20,}"),
    "GitHub fine-grained token": re.compile(rb"github_pat_[A-Za-z0-9_]{20,}"),
    "GitHub classic token": re.compile(rb"gh[opusr]_[A-Za-z0-9]{20,}"),
    "literal R2 access-key ID": re.compile(rb"AWS_ACCESS_KEY_ID\s*=\s*['\"]?[0-9A-Fa-f]{32}(?:['\"]|\s|$)"),
    "literal R2 secret-access key": re.compile(rb"AWS_SECRET_ACCESS_KEY\s*=\s*['\"]?[0-9A-Fa-f]{64}(?:['\"]|\s|$)"),
    "private signing key": re.compile(rb"-----BEGIN (?:(?:PGP|OPENSSH|RSA|EC) )?PRIVATE KEY(?: BLOCK)?-----"),
}

listed = subprocess.run(
    ["git", "ls-files", "-co", "--exclude-standard", "-z"],
    check=True,
    stdout=subprocess.PIPE,
).stdout.split(b"\0")
failures = []
for raw_path in listed:
    if not raw_path:
        continue
    path = pathlib.Path(raw_path.decode("utf-8", "surrogateescape"))
    try:
        data = path.read_bytes()
    except (OSError, ValueError):
        continue
    if b"\0" in data[:8192]:
        continue
    for line_number, line in enumerate(data.splitlines(), 1):
        for label, pattern in rules.items():
            if pattern.search(line):
                failures.append((str(path), line_number, label))

if failures:
    print("Secret hygiene check failed. Matched values are intentionally hidden:")
    for path, line, label in failures:
        print(f"  {path}:{line}: {label}")
    raise SystemExit(1)
print(f"Secret hygiene check passed across {len(listed) - 1} non-ignored source files.")
PY
