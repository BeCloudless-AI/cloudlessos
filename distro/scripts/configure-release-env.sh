#!/usr/bin/env bash
set -euo pipefail

FILE="${CLOUDLESS_RELEASE_ENV:-$HOME/.config/cloudless/release.env}"
DIR="$(dirname "$FILE")"
DEFAULT_SECRET="/mnt/d/CloudlessSecrets/cloudless-archive-secret.asc"
DEFAULT_ENDPOINT="https://bf95ea03816d535ed401e136a3abb3b3.r2.cloudflarestorage.com"
DEFAULT_BUCKET="cloudless-updates"

clean_value() {
    local value="$1"
    value="${value#$'\xEF\xBB\xBF'}"
    if [[ "$value" == \"*\" && "$value" == *\" ]] ||
       [[ "$value" == \'*\' && "$value" == *\' ]]; then
        value="${value:1:${#value}-2}"
    fi
    printf '%s' "$value"
}

read -r -p "R2 endpoint [$DEFAULT_ENDPOINT]: " endpoint
endpoint="${endpoint:-$DEFAULT_ENDPOINT}"
endpoint="$(clean_value "$endpoint")"
read -r -p "R2 bucket [$DEFAULT_BUCKET]: " bucket
bucket="${bucket:-$DEFAULT_BUCKET}"
bucket="$(clean_value "$bucket")"
read -r -p "R2 Access Key ID: " access_key
read -r -s -p "R2 Secret Access Key: " secret_key
printf '\n'
access_key="$(clean_value "$access_key")"
secret_key="$(clean_value "$secret_key")"
read -r -p "Archive signing key [$DEFAULT_SECRET]: " archive_secret
archive_secret="${archive_secret:-$DEFAULT_SECRET}"
archive_secret="$(clean_value "$archive_secret")"
read -r -p "Physical qualification exports directory (optional): " physical_qualification_dir
physical_qualification_dir="$(clean_value "$physical_qualification_dir")"
read -r -p "Security contact attestation file (optional until 1.0): " security_contact_attestation
security_contact_attestation="$(clean_value "$security_contact_attestation")"

[ -n "$access_key" ] || { echo "R2 Access Key ID is required." >&2; exit 1; }
[ -n "$secret_key" ] || { echo "R2 Secret Access Key is required." >&2; exit 1; }
[[ "$endpoint" =~ ^https?://[^[:space:]]+$ ]] || { echo "R2 endpoint must be an http:// or https:// URL." >&2; exit 1; }
[ -s "$archive_secret" ] || { echo "Signing-key backup not found: $archive_secret" >&2; exit 1; }
if [ -n "$physical_qualification_dir" ]; then
    [ -d "$physical_qualification_dir" ] || {
        echo "Physical qualification directory not found: $physical_qualification_dir" >&2
        exit 1
    }
fi
if [ -n "$security_contact_attestation" ]; then
    [ -s "$security_contact_attestation" ] || {
        echo "Security contact attestation not found: $security_contact_attestation" >&2
        exit 1
    }
    attestation_mode="$(stat -c '%a' "$security_contact_attestation" 2>/dev/null || true)"
    case "$attestation_mode" in
        600|400) ;;
        *)
            echo "Security contact attestation must be private (chmod 600): $security_contact_attestation" >&2
            exit 1
            ;;
    esac
fi

umask 077
mkdir -p "$DIR"
tmp="$(mktemp "$DIR/.release.env.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
values=(
    "CLOUDLESS_ARCHIVE_SECRET=$archive_secret" \
    "CLOUDLESS_R2_ENDPOINT=$endpoint" \
    "CLOUDLESS_R2_BUCKET=$bucket" \
    "AWS_ACCESS_KEY_ID=$access_key" \
    "AWS_SECRET_ACCESS_KEY=$secret_key"
)
[ -z "$physical_qualification_dir" ] || values+=("CLOUDLESS_PHYSICAL_QUALIFICATION_DIR=$physical_qualification_dir")
[ -z "$security_contact_attestation" ] || values+=("CLOUDLESS_SECURITY_CONTACT_ATTESTATION=$security_contact_attestation")
printf '%s\n' "${values[@]}" > "$tmp"
chmod 600 "$tmp"
mv -f "$tmp" "$FILE"
trap - EXIT

echo "Private release environment saved to $FILE"
echo "Future releases load it automatically."
