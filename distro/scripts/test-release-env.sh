#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT/distro/scripts/release-env.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
file="$work/release.env"

printf '%s\n' \
    'CLOUDLESS_R2_ENDPOINT=https://example.invalid' \
    'CLOUDLESS_R2_BUCKET=test-bucket' \
    'CLOUDLESS_PHYSICAL_QUALIFICATION_DIR=/private/qualification' \
    'AWS_ACCESS_KEY_ID=test-access' \
    'AWS_SECRET_ACCESS_KEY=test-secret' > "$file"
chmod 600 "$file"

CLOUDLESS_RELEASE_ENV="$file" load_cloudless_release_env
[ "$CLOUDLESS_R2_ENDPOINT" = "https://example.invalid" ]
[ "$CLOUDLESS_R2_BUCKET" = "test-bucket" ]
[ "$CLOUDLESS_PHYSICAL_QUALIFICATION_DIR" = "/private/qualification" ]
[ "$AWS_ACCESS_KEY_ID" = "test-access" ]
[ "$AWS_SECRET_ACCESS_KEY" = "test-secret" ]

printf '%s\n' \
    $'CLOUDLESS_R2_ENDPOINT=\xEF\xBB\xBF"https://quoted.example.invalid"' \
    "CLOUDLESS_R2_BUCKET='quoted-bucket'" > "$file"
chmod 600 "$file"
CLOUDLESS_RELEASE_ENV="$file" load_cloudless_release_env
[ "$CLOUDLESS_R2_ENDPOINT" = "https://quoted.example.invalid" ]
[ "$CLOUDLESS_R2_BUCKET" = "quoted-bucket" ]

chmod 644 "$file"
if CLOUDLESS_RELEASE_ENV="$file" load_cloudless_release_env 2>/dev/null; then
    echo "Public release environment permissions were accepted." >&2
    exit 1
fi

chmod 600 "$file"
printf '%s\n' 'UNSUPPORTED=value' > "$file"
if CLOUDLESS_RELEASE_ENV="$file" load_cloudless_release_env 2>/dev/null; then
    echo "Unsupported release environment key was accepted." >&2
    exit 1
fi

echo "Release environment loader tests passed."
