#!/usr/bin/env bash
# Test, build, sign, atomically publish, and publicly verify one CloudlessOS release.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT/distro/scripts/release-version.sh"
VERSION="${1:-}"
CHANNEL="${2:-stable}"
source "$ROOT/distro/scripts/release-env.sh"
load_cloudless_release_env
SECRET_KEY="${CLOUDLESS_ARCHIVE_SECRET:-/mnt/d/CloudlessSecrets/cloudless-archive-secret.asc}"

usage() {
    echo "Usage: $0 VERSION [stable|beta]" >&2
}
[ -n "$VERSION" ] || { usage; exit 2; }
case "$CHANNEL" in stable|beta) ;; *) usage; exit 2 ;; esac
cloudless_is_release_version "$VERSION" || {
    echo "Release version must be X.Y.Z or X.Y.Z-N (for example 0.2.7-1)." >&2
    exit 2
}

for command in docker find git gpg python3 realpath sort; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done
test -s "$SECRET_KEY" || { echo "Signing-key backup not found: $SECRET_KEY" >&2; exit 1; }
: "${CLOUDLESS_R2_ENDPOINT:?Set CLOUDLESS_R2_ENDPOINT}"
: "${CLOUDLESS_R2_BUCKET:?Set CLOUDLESS_R2_BUCKET}"
: "${AWS_ACCESS_KEY_ID:?Set AWS_ACCESS_KEY_ID}"
: "${AWS_SECRET_ACCESS_KEY:?Set AWS_SECRET_ACCESS_KEY}"
bash "$ROOT/distro/scripts/release-preflight.sh" "$ROOT" "$VERSION" "$CHANNEL" "$SECRET_KEY"

source_version="$(tr -d '[:space:]' < "$ROOT/distro/VERSION")"
cloudless_is_source_version "$source_version" || {
    echo "distro/VERSION is malformed: $source_version" >&2
    exit 1
}
[ "$(cloudless_release_version_from_source "$source_version")" = "$VERSION" ] || {
    echo "distro/VERSION is $source_version; expected $VERSION or $VERSION~dev." >&2
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

physical_output="$ROOT/distro/out/qualification/cloudless-physical-qualification.json"
physical_archives=()
if [ -n "${CLOUDLESS_PHYSICAL_QUALIFICATION_DIR:-}" ]; then
    test -d "$CLOUDLESS_PHYSICAL_QUALIFICATION_DIR" || {
        echo "Physical qualification directory does not exist: $CLOUDLESS_PHYSICAL_QUALIFICATION_DIR" >&2
        exit 1
    }
    mapfile -d '' physical_archives < <(
        find "$CLOUDLESS_PHYSICAL_QUALIFICATION_DIR" -maxdepth 1 -type f -name '*.zip' -print0 | sort -z
    )
    [ "${#physical_archives[@]}" -gt 0 ] || {
        echo "Physical qualification directory contains no sealed ZIP exports." >&2
        exit 1
    }
fi
python3 "$ROOT/distro/scripts/prepare-physical-qualification.py" \
    "$VERSION" "$CHANNEL" "$full_commit" --output "$physical_output" "${physical_archives[@]}"
security_output="$ROOT/distro/out/qualification/cloudless-security-readiness.json"
security_arguments=()
if [ -n "${CLOUDLESS_SECURITY_CONTACT_ATTESTATION:-}" ]; then
    test -s "$CLOUDLESS_SECURITY_CONTACT_ATTESTATION" || {
        echo "Security-contact attestation does not exist: $CLOUDLESS_SECURITY_CONTACT_ATTESTATION" >&2
        exit 1
    }
    security_arguments+=(--attestation "$CLOUDLESS_SECURITY_CONTACT_ATTESTATION")
fi
python3 "$ROOT/distro/scripts/prepare-security-readiness.py" \
    "$VERSION" "$CHANNEL" "$full_commit" --output "$security_output" "${security_arguments[@]}"
ci_output="$ROOT/distro/out/qualification/cloudless-ci-qualification.json"

echo "CloudlessOS production release"
echo "  version: $VERSION"
echo "  channel: $CHANNEL"
echo "  commit:  $commit"
echo "  targets: amd64 + arm64"
read -r -p "Run every gate, sign, publish, and verify this release? [y/N] " answer
case "$answer" in y|Y|yes|YES) ;; *) echo "Cancelled."; exit 0 ;; esac

echo "==> Verifying Docker execution for every release architecture"
bash "$ROOT/distro/scripts/verify-release-container-platforms.sh"

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

echo "==> Local exact-commit qualification (no hosted CI)"
bash "$ROOT/distro/scripts/run-local-qualification.sh" \
    "$VERSION" "$CHANNEL" "$full_commit" "$ci_output"
echo "==> Generating SPDX SBOM for the exact release candidates"
rm -rf "$ROOT/distro/out/security"
mkdir -p "$ROOT/distro/out/security"
CLOUDLESS_SOURCE_COMMIT="$full_commit" \
    python3 "$ROOT/distro/scripts/generate-sbom.py" "$VERSION" \
    "$ROOT/distro/out/security/cloudless-$VERSION.spdx.json"
echo "==> Scanning source dependencies and extracted package payloads"
bash "$ROOT/distro/scripts/scan-vulnerabilities.sh"
echo "==> TEST ONLY: atomic publication simulation"
echo "    Uses version 0.0.0-test-only, a disposable key, temporary packages, and fake.invalid."
echo "    Nothing from this test can enter distro/out or the production R2 bucket."
docker run --rm -v "$ROOT:/src" -w /src \
    cloudless-release-builder bash distro/scripts/test-atomic-repository.sh
echo "==> Binding the validated platform matrix to this exact release"
docker run --rm \
    -e CLOUDLESS_PACKAGE_OUT=/src/distro/out/packages \
    -e CLOUDLESS_SOURCE_COMMIT="$full_commit" \
    -v "$ROOT:/src" -w /src \
    cloudless-package-builder bash distro/scripts/validate-release-matrix.sh "$VERSION" "$CHANNEL"

signed_manifest="$ROOT/distro/out/apt-repository/dists/$CHANNEL/cloudless-release.json"
assert_source_unchanged
reuse_signed=false
if [ -s "$signed_manifest" ] && \
   python3 - "$signed_manifest" "$VERSION" "$commit" "$CHANNEL" <<'PY'
import json, sys
path, version, commit_prefix, channel = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    release = json.load(handle)
valid = release.get("version") == version and str(release.get("sourceCommit", "")).startswith(commit_prefix)
if channel == "stable":
    promotion = release.get("promotion")
    valid = valid and isinstance(promotion, dict) and promotion.get("schema") == "cloudless.beta-promotion.v1"
raise SystemExit(0 if valid else 1)
PY
then
    if CLOUDLESS_PUBLISH_VERIFY_ONLY=1 run_publisher; then
        reuse_signed=true
        echo "==> Resuming the already-signed generation for this exact commit"
    fi
fi
if ! $reuse_signed; then
    if [ "$CHANNEL" = stable ]; then
        echo "==> Verifying the published beta generation selected for byte-identical promotion"
        rm -rf "$ROOT/distro/out/beta-promotion"
        docker run --rm \
            -e CLOUDLESS_APT_PUBLIC_URL \
            -e CLOUDLESS_PUBLIC_ROOT_URL \
            -v "$ROOT:/src" -w /src \
            cloudless-release-builder \
            bash distro/scripts/prepare-beta-promotion.sh \
                "$VERSION" "$full_commit" /src/distro/out/beta-promotion
        export CLOUDLESS_BETA_PROMOTION=/src/distro/out/beta-promotion
    else
        unset CLOUDLESS_BETA_PROMOTION || true
    fi
    echo "==> PRODUCTION: clean rebuild and signing of CloudlessOS $VERSION"
    if [ "$CHANNEL" = stable ]; then
        echo "    Stable metadata is signed around the exact verified beta package and artifact bytes."
    else
        echo "    The beta candidate is rebuilt once inside the protected signing environment."
    fi
    bash "$ROOT/distro/scripts/sign-release-interactive.sh" "$VERSION" "$CHANNEL"
fi
assert_source_unchanged
echo "==> Atomic R2 publication and public verification"
run_publisher

echo "CloudlessOS $VERSION ($CHANNEL) from commit $commit is publicly verified for amd64 and arm64."
