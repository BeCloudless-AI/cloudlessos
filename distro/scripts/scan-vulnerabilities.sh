#!/usr/bin/env bash
# Scan the exact source/package candidate with a pinned Trivy release.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TRIVY_IMAGE="${CLOUDLESS_TRIVY_IMAGE:-aquasec/trivy:0.69.1}"
OUT="${CLOUDLESS_SECURITY_OUT:-$ROOT/distro/out/security}"
PACKAGE_DIR="${CLOUDLESS_PACKAGE_OUT:-$ROOT/distro/out/packages}"

if [ "${1:-}" = "--check" ]; then
    [[ "$TRIVY_IMAGE" =~ ^aquasec/trivy:[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
        echo "The vulnerability scanner image must be pinned to an explicit version." >&2
        exit 1
    }
    grep -Fq -- '--severity HIGH,CRITICAL' "$0"
    grep -Fq -- '--exit-code 1' "$0"
    grep -Fq -- 'dpkg-deb -x' "$0"
    grep -Fq -- '/package-rootfs' "$0"
    echo "Vulnerability scan policy is pinned and fail-closed."
    exit 0
fi

for command in docker dpkg-deb; do
    command -v "$command" >/dev/null || { echo "$command is required for the pinned vulnerability scanner." >&2; exit 1; }
done
mkdir -p "$OUT"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
rootfs="$work/package-rootfs"
mkdir -p "$rootfs"
shopt -s nullglob
packages=("$PACKAGE_DIR"/*.deb)
((${#packages[@]} > 0)) || { echo "No release packages found in $PACKAGE_DIR" >&2; exit 1; }
for package in "${packages[@]}"; do
    dpkg-deb -x "$package" "$rootfs"
done
docker image inspect "$TRIVY_IMAGE" >/dev/null 2>&1 || docker pull "$TRIVY_IMAGE"
docker run --rm -v "$ROOT:/src:ro" -v "$OUT:/reports" "$TRIVY_IMAGE" fs \
    --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 \
    --format json --output /reports/trivy-source.json /src/orchestrator
docker run --rm -v "$rootfs:/package-rootfs:ro" -v "$OUT:/reports" "$TRIVY_IMAGE" fs \
    --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 \
    --format json --output /reports/trivy-packages.json /package-rootfs
echo "Vulnerability policy passed. Reports: $OUT"
