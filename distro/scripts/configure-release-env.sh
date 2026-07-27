#!/usr/bin/env bash
set -euo pipefail

FILE="${CLOUDLESS_RELEASE_ENV:-$HOME/.config/cloudless/release.env}"
DIR="$(dirname "$FILE")"
DEFAULT_SECRET="/mnt/d/CloudlessSecrets/cloudless-archive-secret.asc"
DEFAULT_ENDPOINT="https://bf95ea03816d535ed401e136a3abb3b3.r2.cloudflarestorage.com"
DEFAULT_BUCKET="cloudless-updates"

read -r -p "R2 endpoint [$DEFAULT_ENDPOINT]: " endpoint
endpoint="${endpoint:-$DEFAULT_ENDPOINT}"
read -r -p "R2 bucket [$DEFAULT_BUCKET]: " bucket
bucket="${bucket:-$DEFAULT_BUCKET}"
read -r -p "R2 Access Key ID: " access_key
read -r -s -p "R2 Secret Access Key: " secret_key
printf '\n'
read -r -p "Archive signing key [$DEFAULT_SECRET]: " archive_secret
archive_secret="${archive_secret:-$DEFAULT_SECRET}"

[ -n "$access_key" ] || { echo "R2 Access Key ID is required." >&2; exit 1; }
[ -n "$secret_key" ] || { echo "R2 Secret Access Key is required." >&2; exit 1; }
[ -s "$archive_secret" ] || { echo "Signing-key backup not found: $archive_secret" >&2; exit 1; }

umask 077
mkdir -p "$DIR"
tmp="$(mktemp "$DIR/.release.env.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
printf '%s\n' \
    "CLOUDLESS_ARCHIVE_SECRET=$archive_secret" \
    "CLOUDLESS_R2_ENDPOINT=$endpoint" \
    "CLOUDLESS_R2_BUCKET=$bucket" \
    "AWS_ACCESS_KEY_ID=$access_key" \
    "AWS_SECRET_ACCESS_KEY=$secret_key" > "$tmp"
chmod 600 "$tmp"
mv -f "$tmp" "$FILE"
trap - EXIT

echo "Private release environment saved to $FILE"
echo "Future releases load it automatically."
