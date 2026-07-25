#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
OUT="${CLOUDLESS_PACKAGE_OUT:-$DISTRO/out/packages}"
VERSION="${CLOUDLESS_VERSION:-$(tr -d '[:space:]' < "$DISTRO/VERSION")}"

for command in dpkg-deb file; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done

for arch in amd64 arm64; do
    CLOUDLESS_ARCH="$arch" "$DISTRO/scripts/build-packages.sh"
    for package in cloudless-orchestrator cloudless-shell cloudless-branding cloudless-hardware cloudless-firstboot cloudless-updater; do
        deb="$OUT/${package}_${VERSION}_${arch}.deb"
        test -s "$deb" || { echo "Missing $arch package: $deb" >&2; exit 1; }
        test "$(dpkg-deb -f "$deb" Architecture)" = "$arch"
    done
    root="$(mktemp -d)"
    dpkg-deb -x "$OUT/cloudless-orchestrator_${VERSION}_${arch}.deb" "$root"
    binary_info="$(file "$root/usr/lib/cloudless/cloudlessd")"
    rm -rf "$root"
    case "$arch:$binary_info" in
        amd64:*x86-64*) ;;
        arm64:*ARM\ aarch64*) ;;
        *) echo "Unexpected cloudlessd binary for $arch: $binary_info" >&2; exit 1 ;;
    esac
done

CLOUDLESS_TEST_ARCH=arm64 bash "$DISTRO/scripts/test-package-content.sh"
echo "AMD64 and ARM64 package validation passed"
