#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
VERSION="${CLOUDLESS_VERSION:-$(tr -d '[:space:]' < "$DISTRO/VERSION")}"
UBUNTU_RELEASE="${UBUNTU_RELEASE:-24.04.4}"
UBUNTU_ISO="ubuntu-${UBUNTU_RELEASE}-live-server-amd64.iso"
UBUNTU_URL="${UBUNTU_ISO_URL:-https://releases.ubuntu.com/${UBUNTU_RELEASE}/${UBUNTU_ISO}}"
CACHE="${CLOUDLESS_CACHE_DIR:-$DISTRO/.cache}"
OUT="${CLOUDLESS_ISO_OUT:-$DISTRO/out}"
WORK="${CLOUDLESS_ISO_WORK:-${TMPDIR:-/tmp}/cloudlessos-build-${UID}/iso}"
BASE_ISO="${CLOUDLESS_BASE_ISO:-$CACHE/$UBUNTU_ISO}"
OUTPUT="$OUT/cloudlessos-${VERSION}-amd64.iso"

for command in xorriso curl sha256sum rsvg-convert convert; do command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }; done
mkdir -p "$CACHE" "$OUT" "$WORK"
if [ ! -f "$BASE_ISO" ]; then
    echo "==> Downloading Ubuntu Server $UBUNTU_RELEASE"
    curl --fail --location --continue-at - "$UBUNTU_URL" --output "$BASE_ISO"
fi
if [ -n "${CLOUDLESS_BASE_SHA256:-}" ]; then
    echo "${CLOUDLESS_BASE_SHA256}  ${BASE_ISO}" | sha256sum --check --status
elif [ -z "${CLOUDLESS_BASE_ISO:-}" ]; then
    echo "==> Verifying Ubuntu ISO checksum"
    curl --fail --location "https://releases.ubuntu.com/${UBUNTU_RELEASE}/SHA256SUMS" --output "$CACHE/SHA256SUMS-${UBUNTU_RELEASE}"
    ( cd "$CACHE"; grep -E "[ *]${UBUNTU_ISO}$" "SHA256SUMS-${UBUNTU_RELEASE}" | sha256sum --check --status )
else
    echo "WARNING: custom base ISO was not verified; set CLOUDLESS_BASE_SHA256" >&2
fi
"$DISTRO/scripts/build-packages.sh"

rm -rf "$WORK/overlay" "$WORK/boot"
mkdir -p "$WORK/overlay/cloudless/packages" "$WORK/boot/grub"
cp "$DISTRO"/out/packages/*.deb "$WORK/overlay/cloudless/packages/"
cp "$DISTRO/iso/autoinstall.yaml" "$WORK/overlay/autoinstall.yaml"
cp "$DISTRO/iso/theme.txt" "$WORK/overlay/cloudless/theme.txt"
rsvg-convert -w 1920 -h 1080 "$DISTRO/iso/installer-background.svg" -o "$WORK/installer-background.png"
rsvg-convert -w 700 "$DISTRO/assets/cloudless-logo.svg" -o "$WORK/installer-logo.png"
convert "$WORK/installer-background.png" "$WORK/installer-logo.png" -geometry +610+105 -composite "$WORK/overlay/cloudless/grub.png"
: > "$WORK/overlay/md5sum.txt"

echo "==> Preparing branded installer boot menu"
xorriso -osirrox on -indev "$BASE_ISO" \
    -extract /boot/grub/grub.cfg "$WORK/boot/grub/grub.cfg" \
    -extract /boot/grub/loopback.cfg "$WORK/boot/grub/loopback.cfg" >/dev/null 2>&1
sed -i 's/---/autoinstall ---/g' "$WORK/boot/grub/grub.cfg" "$WORK/boot/grub/loopback.cfg"
sed -i 's/Try or Install Ubuntu Server/Install CloudlessOS/g; s/Ubuntu Server/CloudlessOS/g' "$WORK/boot/grub/grub.cfg" "$WORK/boot/grub/loopback.cfg"
sed -i '/^grub_platform$/d' "$WORK/boot/grub/grub.cfg" "$WORK/boot/grub/loopback.cfg"
cat > "$WORK/boot/cloudless-theme.cfg" <<'EOF'
insmod all_video
insmod gfxterm
insmod png
terminal_output gfxterm
set theme=/cloudless/theme.txt
export theme
EOF
sed -i '/^loadfont unicode$/r '"$WORK/boot/cloudless-theme.cfg" "$WORK/boot/grub/grub.cfg"
cat "$WORK/boot/cloudless-theme.cfg" "$WORK/boot/grub/loopback.cfg" > "$WORK/boot/grub/loopback.cfg.new"
mv "$WORK/boot/grub/loopback.cfg.new" "$WORK/boot/grub/loopback.cfg"

rm -f "$OUTPUT"
echo "==> Repacking hybrid ISO"
xorriso -indev "$BASE_ISO" -outdev "$OUTPUT" -boot_image any replay \
    -map "$WORK/overlay/autoinstall.yaml" /autoinstall.yaml \
    -map "$WORK/overlay/md5sum.txt" /md5sum.txt \
    -map "$WORK/overlay/cloudless" /cloudless \
    -map "$WORK/boot/grub/grub.cfg" /boot/grub/grub.cfg \
    -map "$WORK/boot/grub/loopback.cfg" /boot/grub/loopback.cfg \
    -volid CLOUDLESSOS -commit
( cd "$OUT"; sha256sum "$(basename "$OUTPUT")" > "$(basename "$OUTPUT").sha256" )
echo "==> Built $OUTPUT"
