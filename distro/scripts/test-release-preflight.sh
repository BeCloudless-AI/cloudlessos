#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PREFLIGHT="$ROOT/distro/scripts/release-preflight.sh"
work="$(mktemp -d)"
export GNUPGHOME="$work/gnupg"
trap 'rm -rf "$work"' EXIT
install -d -m 0700 "$GNUPGHOME"

repo="$work/repo"
mkdir -p "$repo/distro/release/keys"
git -C "$work" init -q repo
git -C "$repo" config user.email release-test@becloudless.ai
git -C "$repo" config user.name 'Cloudless Release Test'
cp "$ROOT/distro/release/validation-matrix.json" "$repo/distro/release/validation-matrix.json"
cp "$ROOT/distro/release/keys/community-keys.json" "$repo/distro/release/keys/community-keys.json"
gpg --batch --passphrase '' --quick-generate-key \
    'CloudlessOS Archive <updates@becloudless.ai>' ed25519 sign 1d >/dev/null 2>&1
fingerprint="$(gpg --batch --with-colons --list-keys | awk -F: '$1 == "fpr" {print $10; exit}')"
gpg --batch --armor --export-secret-keys "$fingerprint" > "$work/archive-secret.asc"
printf '%s\n' "$fingerprint" > "$repo/distro/release/keys/cloudless-archive-fingerprint.txt"
git -C "$repo" add .
git -C "$repo" commit -qm baseline

export CLOUDLESS_R2_ENDPOINT=https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com
export CLOUDLESS_R2_BUCKET=cloudless-updates
export AWS_ACCESS_KEY_ID="$(printf '0123456789abcdef%.0s' {1..2})"
export AWS_SECRET_ACCESS_KEY="$(printf '0123456789abcdef%.0s' {1..4})"

bash "$PREFLIGHT" "$repo" 1.2.3-1 stable "$work/archive-secret.asc" >/dev/null
bash "$PREFLIGHT" "$repo" 1.2.3-9a beta "$work/archive-secret.asc" >/dev/null

expect_rejection() {
    local label="$1"
    shift
    if "$@" >/dev/null 2>&1; then
        echo "Release preflight accepted $label" >&2
        exit 1
    fi
}

printf 'dirty\n' > "$repo/untracked-source"
expect_rejection 'a dirty repository' bash "$PREFLIGHT" "$repo" 1.2.3 stable "$work/archive-secret.asc"
rm "$repo/untracked-source"
expect_rejection 'a test version' bash "$PREFLIGHT" "$repo" 0.0.0-test-only stable "$work/archive-secret.asc"
expect_rejection 'a zero Debian revision' bash "$PREFLIGHT" "$repo" 1.2.3-0 stable "$work/archive-secret.asc"
expect_rejection 'a named Debian revision' bash "$PREFLIGHT" "$repo" 1.2.3-hotfix stable "$work/archive-secret.asc"
expect_rejection 'a multi-letter hotfix revision' bash "$PREFLIGHT" "$repo" 1.2.3-9aa beta "$work/archive-secret.asc"
expect_rejection 'an uppercase hotfix revision' bash "$PREFLIGHT" "$repo" 1.2.3-9A beta "$work/archive-secret.asc"
expect_rejection 'a development version' bash "$PREFLIGHT" "$repo" 1.2.3-1~dev stable "$work/archive-secret.asc"

export CLOUDLESS_PROMOTION_MIN_AGE_SECONDS=0
expect_rejection 'a shortened beta soak' bash "$PREFLIGHT" "$repo" 1.2.3 stable "$work/archive-secret.asc"
unset CLOUDLESS_PROMOTION_MIN_AGE_SECONDS

saved_endpoint="$CLOUDLESS_R2_ENDPOINT"
export CLOUDLESS_R2_ENDPOINT=https://fake.invalid
expect_rejection 'a fake endpoint' bash "$PREFLIGHT" "$repo" 1.2.3 stable "$work/archive-secret.asc"
export CLOUDLESS_R2_ENDPOINT="$saved_endpoint"

gpg --batch --passphrase '' --quick-generate-key \
    'Cloudless Publish Test <test@invalid>' ed25519 sign 1d >/dev/null 2>&1
test_fingerprint="$(gpg --batch --with-colons --list-keys | awk -F: '$1 == "fpr" {value=$10} END {print value}')"
gpg --batch --armor --export-secret-keys "$test_fingerprint" > "$work/test-secret.asc"
expect_rejection 'a mismatched/test signing key' bash "$PREFLIGHT" "$repo" 1.2.3 stable "$work/test-secret.asc"

python3 - "$repo/distro/release/validation-matrix.json" <<'PY'
import json, sys
path = sys.argv[1]
with open(path, encoding="utf-8") as handle:
    matrix = json.load(handle)
matrix["requiredGates"].remove("vulnerability-scan")
with open(path, "w", encoding="utf-8") as handle:
    json.dump(matrix, handle)
PY
git -C "$repo" add .
git -C "$repo" commit -qm incomplete-security-gates
expect_rejection 'an incomplete security gate set' bash "$PREFLIGHT" "$repo" 1.2.3 stable "$work/archive-secret.asc"

cp "$ROOT/distro/release/validation-matrix.json" "$repo/distro/release/validation-matrix.json"
python3 - "$repo/distro/release/validation-matrix.json" <<'PY'
import json, sys
path = sys.argv[1]
with open(path, encoding="utf-8") as handle:
    matrix = json.load(handle)
matrix["targets"] = matrix["targets"][:-1]
with open(path, "w", encoding="utf-8") as handle:
    json.dump(matrix, handle)
PY
git -C "$repo" add .
git -C "$repo" commit -qm incomplete-contract
expect_rejection 'an incomplete platform contract' bash "$PREFLIGHT" "$repo" 1.2.3 stable "$work/archive-secret.asc"

echo "Production release preflight tests passed."
