#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
VERSION="${CLOUDLESS_VERSION:-$(tr -d '[:space:]' < "$DISTRO/VERSION")}"
ISO="${1:-$DISTRO/out/cloudlessos-${VERSION}-amd64.iso}"
OUT="${CLOUDLESS_BOOT_TEST_OUT:-$DISTRO/out/boot-tests}"
WORK="${TMPDIR:-/tmp}/cloudlessos-qemu-${UID}"

for command in qemu-system-x86_64 socat convert; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done
test -f "$ISO"
rm -rf "$WORK"
mkdir -p "$WORK" "$OUT"

boot_once() {
    local mode="$1"
    local socket="$WORK/${mode}.sock"
    local ppm="$WORK/${mode}.ppm"
    local -a firmware=()
    if [ "$mode" = uefi ]; then
        local ovmf
        ovmf="$(find /usr/share/OVMF /usr/share/ovmf -iname 'OVMF_CODE.fd' -print -quit 2>/dev/null)"
        test -n "$ovmf" || { echo "OVMF firmware not found" >&2; exit 1; }
        firmware=(-drive "if=pflash,format=raw,readonly=on,file=$ovmf")
    fi

    qemu-system-x86_64 -name "cloudless-${mode}-smoke" -machine q35,accel=tcg \
        -m 2048 -boot d -cdrom "$ISO" "${firmware[@]}" -display none \
        -monitor "unix:$socket,server=on,wait=off" -daemonize
    sleep 12
    printf 'screendump %s\nquit\n' "$ppm" | socat - "UNIX-CONNECT:$socket"
    for _ in $(seq 1 20); do [ -f "$ppm" ] && break; sleep 0.25; done
    test -s "$ppm"
    convert "$ppm" "$OUT/${mode}.png"
    test -s "$OUT/${mode}.png"
    echo "QEMU $mode boot framebuffer captured: $OUT/${mode}.png"
}

boot_once bios
boot_once uefi
echo "QEMU BIOS + UEFI smoke tests passed"
