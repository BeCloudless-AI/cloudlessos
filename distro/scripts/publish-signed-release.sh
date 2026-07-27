#!/usr/bin/env bash
# Resume publication of an already-built and already-signed release.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-}"
CHANNEL="${2:-stable}"
source "$ROOT/distro/scripts/release-env.sh"
load_cloudless_release_env

[ -n "$VERSION" ] || {
    echo "Usage: $0 VERSION [stable|beta]" >&2
    exit 2
}
case "$CHANNEL" in stable|beta) ;; *) echo "Channel must be stable or beta" >&2; exit 2 ;; esac
for command in docker python3; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done
: "${CLOUDLESS_R2_ENDPOINT:?Set CLOUDLESS_R2_ENDPOINT}"
: "${CLOUDLESS_R2_BUCKET:?Set CLOUDLESS_R2_BUCKET}"
: "${AWS_ACCESS_KEY_ID:?Set AWS_ACCESS_KEY_ID}"
: "${AWS_SECRET_ACCESS_KEY:?Set AWS_SECRET_ACCESS_KEY}"
[[ "$CLOUDLESS_R2_ENDPOINT" =~ ^https?://[^[:space:]]+$ ]] || {
    echo "CLOUDLESS_R2_ENDPOINT must be an http:// or https:// URL." >&2
    exit 1
}

manifest="$ROOT/distro/out/apt-repository/dists/$CHANNEL/cloudless-release.json"
test -s "$manifest" || {
    echo "No signed $CHANNEL release is available to publish." >&2
    exit 1
}
source_commit="$(python3 - "$manifest" "$VERSION" "$CHANNEL" <<'PY'
import json, sys
path, expected_version, expected_channel = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    release = json.load(handle)
if release.get("version") != expected_version:
    raise SystemExit(f"Signed release is {release.get('version')}, not {expected_version}")
if release.get("channel") != expected_channel:
    raise SystemExit(f"Signed release channel is {release.get('channel')}, not {expected_channel}")
print(release.get("sourceCommit", "unknown"))
PY
)"

docker image inspect cloudless-release-builder >/dev/null 2>&1 || {
    echo "The release builder image is unavailable; run the normal release command first." >&2
    exit 1
}

echo "CloudlessOS signed-release publication"
echo "  version: $VERSION"
echo "  channel: $CHANNEL"
echo "  commit:  $source_commit"
echo "This verifies the existing signatures and publishes without rebuilding or signing."
read -r -p "Publish this exact signed generation? [y/N] " answer
case "$answer" in y|Y|yes|YES) ;; *) echo "Cancelled."; exit 0 ;; esac

docker run --rm \
    -e AWS_ACCESS_KEY_ID \
    -e AWS_SECRET_ACCESS_KEY \
    -e CLOUDLESS_R2_ENDPOINT \
    -e CLOUDLESS_R2_BUCKET \
    -e CLOUDLESS_APT_PUBLIC_URL \
    -e CLOUDLESS_PUBLIC_ROOT_URL \
    -v "$ROOT:/src" -w /src \
    cloudless-release-builder \
    bash distro/scripts/publish-apt-r2.sh "$CHANNEL"

echo "CloudlessOS $VERSION ($CHANNEL) is publicly verified."
