#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
VERSION="${CLOUDLESS_VERSION:-$(tr -d '[:space:]' < "$DISTRO/VERSION")}"
ISO="${1:-$DISTRO/out/cloudlessos-${VERSION}-amd64.iso}"
OUT="${CLOUDLESS_BOOT_TEST_OUT:-$DISTRO/out/boot-tests}"
WORK="${TMPDIR:-/tmp}/cloudlessos-qemu-${UID}"

for command in qemu-system-x86_64 socat convert compare identify xorriso; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done
test -f "$ISO"
rm -rf "$WORK"
mkdir -p "$WORK" "$OUT"
xorriso -osirrox on -indev "$ISO" -extract /cloudless/grub.png "$WORK/installer-grub.png" >/dev/null 2>&1
test -s "$WORK/installer-grub.png"

boot_once() {
    local mode="$1"
    local socket="$WORK/${mode}.sock"
    local ppm="$WORK/${mode}.ppm"
    local -a firmware=()
    if [ "$mode" = uefi ]; then
        local ovmf_code="" ovmf_vars="" vars_copy="$WORK/uefi-vars.fd"
        for candidate in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd; do
            if [ -f "$candidate" ]; then ovmf_code="$candidate"; break; fi
        done
        for candidate in /usr/share/OVMF/OVMF_VARS_4M.fd /usr/share/OVMF/OVMF_VARS.fd; do
            if [ -f "$candidate" ]; then ovmf_vars="$candidate"; break; fi
        done
        test -n "$ovmf_code" && test -n "$ovmf_vars" || { echo "OVMF code/variable firmware pair not found" >&2; exit 1; }
        cp "$ovmf_vars" "$vars_copy"
        firmware=(
            -drive "if=pflash,format=raw,readonly=on,file=$ovmf_code"
            -drive "if=pflash,format=raw,file=$vars_copy"
        )
    fi

    qemu-system-x86_64 -name "cloudless-${mode}-smoke" -machine q35,accel=tcg \
        -m 2048 -boot d -cdrom "$ISO" "${firmware[@]}" -display none \
        -monitor "unix:$socket,server=on,wait=off" -daemonize
    local boot_wait=12
    # The UEFI GRUB menu has a short countdown. Capture it while the branded
    # installer is visible; a later nonblank framebuffer can be firmware or
    # early-kernel text and is not proof that the branded UEFI path worked.
    if [ "$mode" = uefi ]; then boot_wait=8; fi
    sleep "${CLOUDLESS_BOOT_WAIT_SECONDS:-$boot_wait}"
    printf 'screendump %s\nquit\n' "$ppm" | socat - "UNIX-CONNECT:$socket" >/dev/null
    for _ in $(seq 1 20); do [ -f "$ppm" ] && break; sleep 0.25; done
    test -s "$ppm"
    convert "$ppm" "$OUT/${mode}.png"
    test -s "$OUT/${mode}.png"
    local colors mean dimensions expected expected_crop actual_crop brand_result brand_rmse
    colors="$(convert "$OUT/${mode}.png" -colors 256 -format '%k' info:)"
    mean="$(convert "$OUT/${mode}.png" -format '%[fx:mean]' info:)"
    awk -v colors="$colors" -v mean="$mean" 'BEGIN { exit !(colors >= 8 && mean > 0.01) }' || {
        echo "QEMU $mode framebuffer is blank or visually degenerate (colors=$colors mean=$mean)" >&2
        exit 1
    }
    # A merely nonblank screen can be OVMF text or an early-kernel console. Compare
    # the upper framebuffer to the exact package-owned installer background so
    # the smoke test proves that Cloudless branding actually rendered.
    dimensions="$(identify -format '%wx%h' "$OUT/${mode}.png")"
    expected="$WORK/${mode}-expected.png"
    expected_crop="$WORK/${mode}-expected-top.png"
    actual_crop="$WORK/${mode}-actual-top.png"
    convert "$WORK/installer-grub.png" -resize "${dimensions}!" "$expected"
    convert "$expected" -gravity north -crop '100%x45%+0+0' +repage "$expected_crop"
    convert "$OUT/${mode}.png" -gravity north -crop '100%x45%+0+0' +repage "$actual_crop"
    brand_result="$(compare -metric RMSE "$expected_crop" "$actual_crop" null: 2>&1 || true)"
    brand_rmse="$(printf '%s\n' "$brand_result" | sed -n 's/.*(\([^)]*\)).*/\1/p')"
    awk -v brand_rmse="$brand_rmse" 'BEGIN { exit !(brand_rmse >= 0 && brand_rmse < 0.08) }' || {
        echo "QEMU $mode did not render the packaged Cloudless installer background (RMSE=$brand_rmse)" >&2
        exit 1
    }
    echo "QEMU $mode branded framebuffer captured: $OUT/${mode}.png (RMSE=$brand_rmse)"
}

for mode in ${CLOUDLESS_BOOT_MODES:-bios uefi}; do
    case "$mode" in bios|uefi) boot_once "$mode" ;; *) echo "Unknown boot-test mode: $mode" >&2; exit 2 ;; esac
done
echo "QEMU ${CLOUDLESS_BOOT_MODES:-BIOS + UEFI} branded smoke tests passed"
