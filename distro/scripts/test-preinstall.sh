#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PREFLIGHT="$ROOT/distro/iso/cloudless-preinstall"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

run_preflight() {
    CLOUDLESS_PREFLIGHT_REPORT="$work/$1.txt" \
    CLOUDLESS_PREFLIGHT_ARCH="${2:-amd64}" \
    CLOUDLESS_PREFLIGHT_DISK_BYTES="${3:-137438953472}" \
    CLOUDLESS_PREFLIGHT_FIRMWARE="${4:-uefi}" \
    CLOUDLESS_PREFLIGHT_NETWORK="${5:-ready}" \
    CLOUDLESS_PREFLIGHT_GPU="${6:-nvidia}" \
        bash "$PREFLIGHT"
}

run_preflight supported >/dev/null
grep -q '^SUMMARY errors=0 warnings=0 ' "$work/supported.txt"

run_preflight degraded amd64 68719476736 bios offline virtual >/dev/null
grep -q '^SUMMARY errors=0 warnings=' "$work/degraded.txt"
grep -q 'base system can install from the USB' "$work/degraded.txt"

if run_preflight wrong-arch arm64 >/dev/null 2>&1; then
    echo "AMD64 ISO preflight accepted ARM64" >&2
    exit 1
fi
grep -q 'DGX Spark uses the ARM64 package-layer installer' "$work/wrong-arch.txt"

if run_preflight tiny-disk amd64 17179869184 >/dev/null 2>&1; then
    echo "Installer preflight accepted a disk below its minimum" >&2
    exit 1
fi
grep -q 'No writable disk of at least 32 GiB' "$work/tiny-disk.txt"

echo "Installer preflight tests passed."
