#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
VERSION="${CLOUDLESS_VERSION:-$(tr -d '[:space:]' < "$DISTRO/VERSION")}"
ISO="${1:-$DISTRO/out/cloudlessos-${VERSION}-amd64.iso}"
CHECKSUM="$ISO.sha256"
WORK="${TMPDIR:-/tmp}/cloudlessos-iso-test-${UID}"

command -v xorriso >/dev/null || { echo "Missing required command: xorriso" >&2; exit 1; }
test -f "$ISO"
test -f "$CHECKSUM"
( cd "$(dirname "$ISO")"; sha256sum --check "$(basename "$CHECKSUM")" )

REPORT="$(xorriso -indev "$ISO" -report_el_torito plain 2>&1)"
grep -q 'BIOS' <<<"$REPORT"
grep -q 'UEFI' <<<"$REPORT"

rm -rf "$WORK"
mkdir -p "$WORK"
xorriso -osirrox on -indev "$ISO" \
    -extract /autoinstall.yaml "$WORK/autoinstall.yaml" \
    -extract /boot/grub/grub.cfg "$WORK/grub.cfg" \
    -extract /boot/grub/loopback.cfg "$WORK/loopback.cfg" \
    -extract /cloudless/packages "$WORK/packages" >/dev/null 2>&1

grep -q '^autoinstall:' "$WORK/autoinstall.yaml"
grep -q 'autoinstall ---' "$WORK/grub.cfg"
grep -q 'CloudlessOS' "$WORK/grub.cfg"
grep -q 'set theme=/cloudless/theme.txt' "$WORK/grub.cfg"
test "$(find "$WORK/packages" -name '*.deb' | wc -l)" -eq 6
for name in orchestrator shell branding hardware firstboot updater; do
    find "$WORK/packages" -name "cloudless-${name}_*.deb" -print -quit | grep -q .
done

echo "ISO validation passed: BIOS + UEFI boot catalog, autoinstall, branding, and six packages"
