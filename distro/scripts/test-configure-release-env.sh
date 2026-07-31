#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIGURE="$ROOT/distro/scripts/configure-release-env.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

secret="$work/archive-secret.asc"
qualification="$work/qualification"
attestation="$work/security-contact.json"
file="$work/config/release.env"
printf 'test-only signing key fixture\n' > "$secret"
mkdir -p "$qualification"
printf '{"schema":"cloudless.security-contact-attestation.v1"}\n' > "$attestation"
chmod 600 "$attestation"

printf '%s\n' \
    'https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com' \
    'cloudless-updates' \
    'test-access-key' \
    'test-secret-key' \
    "$secret" \
    "$qualification" \
    "$attestation" | \
    CLOUDLESS_RELEASE_ENV="$file" bash "$CONFIGURE" >/dev/null

[ "$(stat -c '%a' "$file")" = 600 ]
grep -Fxq "CLOUDLESS_PHYSICAL_QUALIFICATION_DIR=$qualification" "$file"
grep -Fxq "CLOUDLESS_SECURITY_CONTACT_ATTESTATION=$attestation" "$file"

unset CLOUDLESS_ARCHIVE_SECRET CLOUDLESS_R2_ENDPOINT CLOUDLESS_R2_BUCKET
unset CLOUDLESS_PHYSICAL_QUALIFICATION_DIR CLOUDLESS_SECURITY_CONTACT_ATTESTATION
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
source "$ROOT/distro/scripts/release-env.sh"
CLOUDLESS_RELEASE_ENV="$file" load_cloudless_release_env
[ "$CLOUDLESS_ARCHIVE_SECRET" = "$secret" ]
[ "$CLOUDLESS_PHYSICAL_QUALIFICATION_DIR" = "$qualification" ]
[ "$CLOUDLESS_SECURITY_CONTACT_ATTESTATION" = "$attestation" ]

chmod 644 "$attestation"
if printf '%s\n' \
    'https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com' \
    'cloudless-updates' 'test-access-key' 'test-secret-key' "$secret" '' "$attestation" | \
    CLOUDLESS_RELEASE_ENV="$work/rejected.env" bash "$CONFIGURE" >/dev/null 2>&1; then
    echo "Configurator accepted a publicly readable security attestation." >&2
    exit 1
fi

echo "One-time release environment configuration tests passed."
