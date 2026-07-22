#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REPO="${CLOUDLESS_APT_REPO_OUT:-$ROOT/distro/out/apt-repository}"
: "${CLOUDLESS_R2_ENDPOINT:?Set CLOUDLESS_R2_ENDPOINT}"
: "${CLOUDLESS_R2_BUCKET:?Set CLOUDLESS_R2_BUCKET}"
command -v aws >/dev/null || { echo "Install the AWS CLI first." >&2; exit 1; }
test -s "$REPO/dists/stable/InRelease" -o -s "$REPO/dists/beta/InRelease" || {
    echo "No signed repository found at $REPO" >&2
    exit 1
}
aws s3 sync "$REPO/" "s3://$CLOUDLESS_R2_BUCKET/apt/" \
    --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --exclude 'conf/*' --exclude 'db/*' --delete
aws s3 rm "s3://$CLOUDLESS_R2_BUCKET/apt/conf/" \
    --endpoint-url "$CLOUDLESS_R2_ENDPOINT" --recursive >/dev/null
aws s3 rm "s3://$CLOUDLESS_R2_BUCKET/apt/db/" \
    --endpoint-url "$CLOUDLESS_R2_ENDPOINT" --recursive >/dev/null
echo "Published to https://updates.becloudless.ai/apt"
