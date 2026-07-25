#!/usr/bin/env bash
# Test, build, sign, atomically publish, and publicly verify one CloudlessOS release.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-}"
CHANNEL="${2:-stable}"
SECRET_KEY="${CLOUDLESS_ARCHIVE_SECRET:-/mnt/d/CloudlessSecrets/cloudless-archive-secret.asc}"

usage() {
    echo "Usage: $0 VERSION [stable|beta]" >&2
}
[ -n "$VERSION" ] || { usage; exit 2; }
case "$CHANNEL" in stable|beta) ;; *) usage; exit 2 ;; esac
[[ "$VERSION" != *dev* ]] || { echo "Development versions cannot be published." >&2; exit 2; }

for command in docker git python3; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done
test -s "$SECRET_KEY" || { echo "Signing-key backup not found: $SECRET_KEY" >&2; exit 1; }
: "${CLOUDLESS_R2_ENDPOINT:?Set CLOUDLESS_R2_ENDPOINT}"
: "${CLOUDLESS_R2_BUCKET:?Set CLOUDLESS_R2_BUCKET}"
: "${AWS_ACCESS_KEY_ID:?Set AWS_ACCESS_KEY_ID}"
: "${AWS_SECRET_ACCESS_KEY:?Set AWS_SECRET_ACCESS_KEY}"

source_version="$(tr -d '[:space:]' < "$ROOT/distro/VERSION")"
[ "${source_version%-dev}" = "$VERSION" ] || {
    echo "distro/VERSION is $source_version; expected $VERSION or $VERSION-dev." >&2
    exit 1
}
test -s "$ROOT/distro/release/notes/$VERSION.json" || {
    echo "Missing release notes: distro/release/notes/$VERSION.json" >&2
    exit 1
}

git -C "$ROOT" diff --quiet && git -C "$ROOT" diff --cached --quiet || {
    echo "Tracked source changes must be committed before a production release." >&2
    exit 1
}
upstream="$(git -C "$ROOT" rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null || true)"
[ -n "$upstream" ] || { echo "The current branch has no pushed upstream." >&2; exit 1; }
[ "$(git -C "$ROOT" rev-parse HEAD)" = "$(git -C "$ROOT" rev-parse "$upstream")" ] || {
    echo "Push the current commit to $upstream before releasing it." >&2
    exit 1
}

commit="$(git -C "$ROOT" rev-parse --short=12 HEAD)"
full_commit="$(git -C "$ROOT" rev-parse HEAD)"
assert_source_unchanged() {
    [ "$(git -C "$ROOT" rev-parse HEAD)" = "$full_commit" ] &&
        git -C "$ROOT" diff --quiet &&
        git -C "$ROOT" diff --cached --quiet || {
        echo "Tracked source changed during the release; refusing to continue." >&2
        exit 1
    }
}
echo "CloudlessOS production release"
echo "  version: $VERSION"
echo "  channel: $CHANNEL"
echo "  commit:  $commit"
echo "  targets: amd64 + arm64"
read -r -p "Run every gate, sign, publish, and verify this release? [y/N] " answer
case "$answer" in y|Y|yes|YES) ;; *) echo "Cancelled."; exit 0 ;; esac

docker image inspect cloudless-release-builder >/dev/null 2>&1 || \
    docker build -t cloudless-release-builder \
        -f "$ROOT/distro/docker/release-builder/Dockerfile" "$ROOT"
docker image inspect cloudless-package-builder >/dev/null 2>&1 || \
    docker build -t cloudless-package-builder \
        -f "$ROOT/distro/docker/package-builder/Dockerfile" "$ROOT"
docker image inspect node:22-bookworm >/dev/null 2>&1 || docker pull node:22-bookworm

run_publisher() {
    docker run --rm \
        -e AWS_ACCESS_KEY_ID \
        -e AWS_SECRET_ACCESS_KEY \
        -e CLOUDLESS_R2_ENDPOINT \
        -e CLOUDLESS_R2_BUCKET \
        -e CLOUDLESS_APT_PUBLIC_URL \
        -e CLOUDLESS_PUBLIC_ROOT_URL \
        -e CLOUDLESS_PUBLISH_VERIFY_ONLY="${CLOUDLESS_PUBLISH_VERIFY_ONLY:-0}" \
        -v "$ROOT:/src" -w /src \
        cloudless-release-builder \
        bash distro/scripts/publish-apt-r2.sh "$CHANNEL"
}

echo "==> Go tests"
docker run --rm -v "$ROOT:/src" -w /src/orchestrator \
    cloudless-release-builder go test ./...
echo "==> Browser JavaScript syntax"
docker run --rm -v "$ROOT:/src" -w /src \
    node:22-bookworm node distro/scripts/test-web-js.js
echo "==> AMD64 and ARM64 package validation"
docker run --rm -v "$ROOT:/src" -w /src \
    cloudless-package-builder bash distro/scripts/test-architectures.sh
docker run --rm -v "$ROOT:/src" -w /src \
    cloudless-package-builder bash distro/scripts/test-packages.sh
echo "==> Atomic repository and standalone-artifact publication test"
docker run --rm -v "$ROOT:/src" -w /src \
    cloudless-release-builder bash distro/scripts/test-atomic-repository.sh

signed_manifest="$ROOT/distro/out/apt-repository/dists/$CHANNEL/cloudless-release.json"
assert_source_unchanged
reuse_signed=false
if [ -s "$signed_manifest" ] && \
   python3 - "$signed_manifest" "$VERSION" "$commit" <<'PY'
import json, sys
path, version, commit_prefix = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    release = json.load(handle)
raise SystemExit(0 if release.get("version") == version and
                 str(release.get("sourceCommit", "")).startswith(commit_prefix) else 1)
PY
then
    if CLOUDLESS_PUBLISH_VERIFY_ONLY=1 run_publisher; then
        reuse_signed=true
        echo "==> Resuming the already-signed generation for this exact commit"
    fi
fi
if ! $reuse_signed; then
    echo "==> Production signing"
    bash "$ROOT/distro/scripts/sign-release-interactive.sh" "$VERSION" "$CHANNEL"
fi
assert_source_unchanged
echo "==> Atomic R2 publication and public verification"
run_publisher

echo "CloudlessOS $VERSION ($CHANNEL) from commit $commit is publicly verified for amd64 and arm64."
