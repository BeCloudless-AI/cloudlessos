#!/usr/bin/env bash
# Fail-fast production release identity and secret validation. This script does
# not import the archive secret key and never prints credential values.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/release-version.sh"

ROOT="$(realpath -e "${1:?repository root is required}")"
VERSION="${2:?release version is required}"
CHANNEL="${3:?release channel is required}"
SECRET_KEY="$(realpath -e "${4:?archive secret key is required}")"

fail() {
    echo "Release preflight: $*" >&2
    exit 1
}

for command in git gpg python3 realpath; do
    command -v "$command" >/dev/null || fail "missing required command: $command"
done

[ -z "${GIT_DIR:-}" ] && [ -z "${GIT_WORK_TREE:-}" ] || \
    fail "GIT_DIR/GIT_WORK_TREE overrides are not allowed"
[ "$(git -C "$ROOT" rev-parse --is-inside-work-tree 2>/dev/null)" = true ] || \
    fail "repository root is not a Git worktree"
git_root="$(realpath -e "$(git -C "$ROOT" rev-parse --show-toplevel)")"
[ "$git_root" = "$ROOT" ] || fail "repository root is ambiguous: expected $ROOT, Git selected $git_root"
[ -z "$(git -C "$ROOT" status --porcelain --untracked-files=normal)" ] || \
    fail "repository contains tracked or untracked source changes"

cloudless_is_release_version "$VERSION" || \
    fail "version must be X.Y.Z or Debian revision X.Y.Z-N with an optional hotfix letter (for example 0.2.7-9a)"
case "$CHANNEL" in stable|beta) ;; *) fail "channel must be stable or beta" ;; esac
[ "${CLOUDLESS_PROMOTION_MIN_AGE_SECONDS:-604800}" = 604800 ] || \
    fail "production beta-to-stable promotion requires the full seven-day soak"
[ "${CLOUDLESS_ARCHES:-amd64 arm64}" = "amd64 arm64" ] || \
    fail "production releases must contain exactly amd64 and arm64"

: "${CLOUDLESS_R2_ENDPOINT:?Release preflight: CLOUDLESS_R2_ENDPOINT is required}"
: "${CLOUDLESS_R2_BUCKET:?Release preflight: CLOUDLESS_R2_BUCKET is required}"
: "${AWS_ACCESS_KEY_ID:?Release preflight: AWS_ACCESS_KEY_ID is required}"
: "${AWS_SECRET_ACCESS_KEY:?Release preflight: AWS_SECRET_ACCESS_KEY is required}"
[[ "$CLOUDLESS_R2_ENDPOINT" =~ ^https://[0-9a-f]{32}\.r2\.cloudflarestorage\.com/?$ ]] || \
    fail "R2 endpoint must be the account-scoped Cloudflare HTTPS endpoint"
[[ "$CLOUDLESS_R2_BUCKET" =~ ^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$ ]] || \
    fail "R2 bucket name is invalid"
case "${CLOUDLESS_R2_BUCKET,,}" in *test*|*fake*|*example*|*invalid*) fail "test/fake R2 buckets cannot publish production releases" ;; esac
[[ "$AWS_ACCESS_KEY_ID" =~ ^[0-9a-fA-F]{32}$ ]] || fail "R2 access-key ID format is invalid"
[[ "$AWS_SECRET_ACCESS_KEY" =~ ^[0-9a-fA-F]{64}$ ]] || fail "R2 secret-access-key format is invalid"

fingerprint_file="$ROOT/distro/release/keys/cloudless-archive-fingerprint.txt"
community_keyring="$ROOT/distro/release/keys/community-keys.json"
matrix="$ROOT/distro/release/validation-matrix.json"
[ -s "$fingerprint_file" ] || fail "archive fingerprint contract is missing"
[ -s "$community_keyring" ] || fail "community recipe trust root is missing"
[ -s "$matrix" ] || fail "platform validation contract is missing"
expected_fingerprint="$(tr -d '[:space:]' < "$fingerprint_file")"
[[ "$expected_fingerprint" =~ ^[0-9A-Fa-f]{40}$ ]] || fail "archive fingerprint contract is malformed"
key_listing="$(gpg --batch --with-colons --show-keys "$SECRET_KEY" 2>/dev/null)" || \
    fail "archive secret key cannot be inspected"
actual_fingerprint="$(printf '%s\n' "$key_listing" | awk -F: '$1 == "fpr" {print $10; exit}')"
[ "${actual_fingerprint^^}" = "${expected_fingerprint^^}" ] || \
    fail "archive secret key does not match the committed production trust root"
uid="$(printf '%s\n' "$key_listing" | awk -F: '$1 == "uid" {print tolower($10); exit}')"
case "$uid" in *test*|*invalid*|*example*) fail "test signing identities cannot publish production releases" ;; esac
case "$uid" in *updates@becloudless.ai*) ;; *) fail "archive signing identity is not the CloudlessOS update identity" ;; esac

python3 - "$community_keyring" <<'PY'
import base64, json, re, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    document = json.load(handle)
if document.get("schema") != "cloudless.community.keys/v1":
    raise SystemExit("Release preflight: unsupported community keyring schema")
keys = document.get("keys")
if not isinstance(keys, list) or not keys:
    raise SystemExit("Release preflight: community keyring contains no trusted key")
seen = set()
for key in keys:
    identifier = key.get("id", "")
    if not re.fullmatch(r"[a-z0-9-]{3,80}", identifier) or identifier in seen:
        raise SystemExit("Release preflight: community key identifier is malformed or duplicated")
    seen.add(identifier)
    try:
        der = base64.b64decode(key.get("publicKey", ""), validate=True)
    except Exception as error:
        raise SystemExit("Release preflight: community public key is malformed") from error
    if len(der) != 44 or not der.startswith(bytes.fromhex("302a300506032b6570032100")):
        raise SystemExit("Release preflight: community trust root is not an Ed25519 SPKI key")
PY

python3 - "$matrix" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    matrix = json.load(handle)
if matrix.get("schema") != "cloudless.release-validation.v1":
    raise SystemExit("Release preflight: unsupported platform validation schema")
targets = {(item.get("platform"), item.get("architecture")) for item in matrix.get("targets", [])}
required = {("generic", "amd64"), ("generic", "arm64"), ("dgx-spark", "arm64")}
if targets != required:
    raise SystemExit("Release preflight: platform validation targets are incomplete or ambiguous")
required_gates = {
    "go-tests", "go-vet", "web-javascript", "app-manifest-v2", "backup-recovery", "installer-preflight", "platform-matrix",
    "package-architecture", "package-contents", "package-lifecycle", "release-isolation", "release-preflight",
    "secret-hygiene", "service-hardening", "sbom", "trust-inventory", "vulnerability-scan",
    "updater-workload-continuity", "atomic-repository",
}
if set(matrix.get("requiredGates", [])) != required_gates:
    raise SystemExit("Release preflight: required release gates are incomplete or ambiguous")
PY

echo "Production release preflight passed for $VERSION ($CHANNEL)."
