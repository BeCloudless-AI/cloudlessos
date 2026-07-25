#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
VERSION="${CLOUDLESS_VERSION:-$(tr -d '[:space:]' < "$DISTRO/VERSION")}"
ARCH="${CLOUDLESS_ARCH:-amd64}"
OUT="${CLOUDLESS_PACKAGE_OUT:-$DISTRO/out/packages}"
WORK="${CLOUDLESS_BUILD_DIR:-${TMPDIR:-/tmp}/cloudlessos-build-${UID}/packages-$ARCH}"
export GOFLAGS="${GOFLAGS:--buildvcs=false -trimpath}"
case "$ARCH" in
    amd64|arm64) ;;
    *) echo "Unsupported package architecture: $ARCH (expected amd64 or arm64)" >&2; exit 2 ;;
esac
for command in dpkg-deb go rsvg-convert convert; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done
rm -rf "$WORK"
mkdir -p "$WORK" "$OUT"
rm -f "$OUT"/cloudless-*_"$ARCH".deb

echo "==> Building cloudlessd $VERSION for linux/$ARCH"
( cd "$ROOT/orchestrator"; CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -ldflags="-s -w" -o "$WORK/cloudlessd" ./cmd/cloudlessd )
( cd "$ROOT/orchestrator"; CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -ldflags="-s -w" -o "$WORK/cloudless-updater-bin" ./cmd/cloudless-updater )

make_control() {
    local root="$1" package="$2" description="$3" depends="$4"
    mkdir -p "$root/DEBIAN"
    cat > "$root/DEBIAN/control" <<EOF
Package: $package
Version: $VERSION
Section: admin
Priority: optional
Architecture: $ARCH
Maintainer: Cloudless <hello@becloudless.ai>
Depends: $depends
Description: $description
EOF
}
finish_package() {
    local root="$1" package="$2"
    find "$root" -type d -exec chmod 0755 {} +
    dpkg-deb --root-owner-group --build "$root" "$OUT/${package}_${VERSION}_${ARCH}.deb"
}

PKG="$WORK/cloudless-orchestrator"
make_control "$PKG" cloudless-orchestrator "CloudlessOS local AI orchestrator" "docker.io | docker-ce, ca-certificates, gpgv"
install -Dm0755 "$WORK/cloudlessd" "$PKG/usr/lib/cloudless/cloudlessd"
install -Dm0644 "$DISTRO/release/keys/cloudless-archive-keyring.pgp" \
    "$PKG/usr/share/cloudless/cloudless-archive-keyring.pgp"
install -Dm0644 "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service" "$PKG/lib/systemd/system/cloudlessd.service"
install -Dm0644 "$DISTRO/packages/cloudless-orchestrator/cloudless.env" "$PKG/etc/cloudless/cloudless.env"
install -Dm0755 "$DISTRO/packages/cloudless-orchestrator/postinst" "$PKG/DEBIAN/postinst"
install -Dm0755 "$DISTRO/packages/cloudless-orchestrator/prerm" "$PKG/DEBIAN/prerm"
finish_package "$PKG" cloudless-orchestrator

PKG="$WORK/cloudless-shell"
make_control "$PKG" cloudless-shell "CloudlessOS fullscreen web shell" "lightdm, lightdm-gtk-greeter, openbox, pcmanfm, xorg, curl, feh, unclutter, x11-xserver-utils"
install -Dm0755 "$DISTRO/packages/cloudless-shell/cloudless-kiosk" "$PKG/usr/bin/cloudless-kiosk"
install -Dm0755 "$DISTRO/packages/cloudless-shell/cloudless-dgx-desktop-mode" "$PKG/usr/sbin/cloudless-dgx-desktop-mode"
install -Dm0644 "$DISTRO/packages/cloudless-shell/openbox-autostart" "$PKG/usr/share/cloudless/openbox-autostart"
install -Dm0644 "$DISTRO/packages/cloudless-shell/lightdm.conf" "$PKG/etc/lightdm/lightdm.conf.d/60-cloudless.conf"
mkdir -p "$PKG/usr/share/backgrounds/cloudless"
rsvg-convert -w 1920 -h 1080 "$DISTRO/packages/cloudless-shell/startup-background.svg" -o "$WORK/startup-background.png"
rsvg-convert -w 560 "$DISTRO/assets/cloudless-logo.svg" -o "$WORK/startup-logo.png"
convert "$WORK/startup-background.png" "$WORK/startup-logo.png" -gravity center -composite "$PKG/usr/share/backgrounds/cloudless/startup.png"
install -Dm0755 "$DISTRO/packages/cloudless-shell/postinst" "$PKG/DEBIAN/postinst"
finish_package "$PKG" cloudless-shell

PKG="$WORK/cloudless-branding"
make_control "$PKG" cloudless-branding "CloudlessOS boot and system branding" "plymouth, initramfs-tools, grub-common, base-files"
install -Dm0644 "$DISTRO/packages/cloudless-branding/cloudless.plymouth" "$PKG/usr/share/plymouth/themes/cloudless/cloudless.plymouth"
install -Dm0644 "$DISTRO/packages/cloudless-branding/cloudless.script" "$PKG/usr/share/plymouth/themes/cloudless/cloudless.script"
rsvg-convert -w 640 "$DISTRO/assets/cloudless-logo.svg" -o "$PKG/usr/share/plymouth/themes/cloudless/cloudless-logo.png"
mkdir -p "$PKG/usr/share/backgrounds/cloudless"
rsvg-convert -w 1920 -h 1080 "$DISTRO/packages/cloudless-branding/grub-background.svg" -o "$WORK/grub-background.png"
rsvg-convert -w 700 "$DISTRO/assets/cloudless-logo.svg" -o "$WORK/grub-logo.png"
convert "$WORK/grub-background.png" "$WORK/grub-logo.png" -geometry +610+340 -composite "$PKG/usr/share/backgrounds/cloudless/grub.png"
install -Dm0644 "$DISTRO/packages/cloudless-branding/99-cloudless.cfg" "$PKG/etc/default/grub.d/99-cloudless.cfg"
install -Dm0644 "$DISTRO/packages/cloudless-branding/os-release" "$PKG/usr/share/cloudless/os-release"
install -Dm0755 "$DISTRO/packages/cloudless-branding/postinst" "$PKG/DEBIAN/postinst"
install -Dm0755 "$DISTRO/packages/cloudless-branding/prerm" "$PKG/DEBIAN/prerm"
finish_package "$PKG" cloudless-branding

PKG="$WORK/cloudless-hardware"
make_control "$PKG" cloudless-hardware "CloudlessOS NVIDIA and container hardware setup" "ubuntu-drivers-common, pciutils, curl, ca-certificates, gnupg"
install -Dm0755 "$DISTRO/packages/cloudless-hardware/cloudless-hardware-install" "$PKG/usr/lib/cloudless/cloudless-hardware-install"
install -Dm0644 "$DISTRO/packages/cloudless-hardware/cloudless-hardware.service" "$PKG/lib/systemd/system/cloudless-hardware.service"
install -Dm0755 "$DISTRO/packages/cloudless-hardware/postinst" "$PKG/DEBIAN/postinst"
finish_package "$PKG" cloudless-hardware

PKG="$WORK/cloudless-firstboot"
make_control "$PKG" cloudless-firstboot "CloudlessOS first-boot validation" "curl"
install -Dm0755 "$DISTRO/packages/cloudless-firstboot/cloudless-validate" "$PKG/usr/lib/cloudless/cloudless-validate"
install -Dm0755 "$DISTRO/packages/cloudless-firstboot/cloudless-diagnostics" "$PKG/usr/bin/cloudless-diagnostics"
install -Dm0644 "$DISTRO/packages/cloudless-firstboot/cloudless-firstboot.service" "$PKG/lib/systemd/system/cloudless-firstboot.service"
install -Dm0755 "$DISTRO/packages/cloudless-firstboot/postinst" "$PKG/DEBIAN/postinst"
finish_package "$PKG" cloudless-firstboot

PKG="$WORK/cloudless-updater"
make_control "$PKG" cloudless-updater "CloudlessOS signed system and NVIDIA driver updater" "apt, ca-certificates, gpgv, ubuntu-drivers-common, pciutils"
install -Dm0755 "$WORK/cloudless-updater-bin" "$PKG/usr/lib/cloudless/cloudless-updater"
install -Dm0644 "$DISTRO/packages/cloudless-updater/cloudless.sources" "$PKG/etc/apt/sources.list.d/cloudless.sources"
install -Dm0644 "$DISTRO/packages/cloudless-updater/cloudless-update-check.service" "$PKG/lib/systemd/system/cloudless-update-check.service"
install -Dm0644 "$DISTRO/packages/cloudless-updater/cloudless-update-check.timer" "$PKG/lib/systemd/system/cloudless-update-check.timer"
install -Dm0644 "$DISTRO/packages/cloudless-updater/cloudless-update-apply.service" "$PKG/lib/systemd/system/cloudless-update-apply.service"
install -Dm0644 "$DISTRO/packages/cloudless-updater/cloudless-nvidia-check.service" "$PKG/lib/systemd/system/cloudless-nvidia-check.service"
install -Dm0644 "$DISTRO/packages/cloudless-updater/cloudless-nvidia-check.timer" "$PKG/lib/systemd/system/cloudless-nvidia-check.timer"
install -Dm0644 "$DISTRO/packages/cloudless-updater/cloudless-nvidia-apply.service" "$PKG/lib/systemd/system/cloudless-nvidia-apply.service"
install -Dm0755 "$DISTRO/packages/cloudless-updater/postinst" "$PKG/DEBIAN/postinst"
install -Dm0755 "$DISTRO/packages/cloudless-updater/prerm" "$PKG/DEBIAN/prerm"
if [ -s "$DISTRO/release/keys/cloudless-archive-keyring.pgp" ]; then
    install -Dm0644 "$DISTRO/release/keys/cloudless-archive-keyring.pgp" "$PKG/usr/share/keyrings/cloudless-archive-keyring.pgp"
    sed -i 's/^Enabled: no$/Enabled: yes/' "$PKG/etc/apt/sources.list.d/cloudless.sources"
fi
finish_package "$PKG" cloudless-updater
echo "==> Packages written to $OUT"
