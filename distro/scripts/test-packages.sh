#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
"$DISTRO/scripts/build-packages.sh"
for package in "$DISTRO"/out/packages/*.deb; do
    echo "==> Checking $(basename "$package")"
    dpkg-deb --info "$package" >/dev/null
    dpkg-deb --contents "$package" >/dev/null
done

# os-release is parsed as shell-style key/value data by cloud-init and systemd.
# A malformed token here breaks cloud-init-local before networking is available.
while IFS= read -r line; do
    case "$line" in
        ''|'#'*) continue ;;
        [A-Z_]*=*) ;;
        *) echo "Invalid os-release line: $line" >&2; exit 1 ;;
    esac
done < "$DISTRO/packages/cloudless-branding/os-release"
sh -n "$DISTRO/packages/cloudless-branding/os-release"

# Chromium is a strictly confined snap on Ubuntu. A profile under ~/.config
# causes an EACCES failure on SingletonLock and leaves only Openbox's black
# background visible.
grep -Fq '$HOME/snap/chromium/common/cloudless-browser' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'startup.png' "$DISTRO/packages/cloudless-shell/openbox-autostart"
grep -Fq 'pcmanfm' "$DISTRO/scripts/build-packages.sh"
grep -Fq 'CLOUDLESS_HOME=/home/cloudless/Cloudless' \
    "$DISTRO/packages/cloudless-orchestrator/cloudless.env"
grep -Fq 'CLOUDLESS_DESKTOP_HOME=/home/cloudless' \
    "$DISTRO/packages/cloudless-orchestrator/cloudless.env"
grep -Fq 'capabilities.BuildVersion=$VERSION' "$DISTRO/scripts/build-packages.sh"
orchestrator_deb="$(find "$DISTRO/out/packages" -maxdepth 1 -type f -name 'cloudless-orchestrator_*_amd64.deb' -print -quit)"
test -n "$orchestrator_deb"
dpkg-deb -f "$orchestrator_deb" Depends | grep -Eq '(^|, )gpgv(,|$)'
dpkg-deb -c "$orchestrator_deb" |
    grep -F './usr/share/cloudless/cloudless-archive-keyring.pgp' >/dev/null
grep -Fq 'update-alternatives --set default.plymouth' \
    "$DISTRO/packages/cloudless-branding/postinst"
grep -Fq 'Wants=docker.service' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"
for netplan_runtime_path in /etc/netplan /run/systemd/system /run/systemd/network /run/NetworkManager/conf.d /run/NetworkManager/system-connections /run/udev; do
    grep -Fq "$netplan_runtime_path" "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service" || {
        echo "cloudlessd sandbox blocks Netplan runtime path: $netplan_runtime_path" >&2
        exit 1
    }
done
grep -Fq 'updates.becloudless.ai/apt' "$DISTRO/packages/cloudless-updater/cloudless.sources"
grep -Fq 'cloudless-updater apply' "$DISTRO/packages/cloudless-updater/cloudless-update-apply.service"
grep -Fq 'cloudless-updater check' "$DISTRO/packages/cloudless-updater/cloudless-update-check.service"
grep -Fq 'cloudless-updater nvidia-check' "$DISTRO/packages/cloudless-updater/cloudless-nvidia-check.service"
grep -Fq 'cloudless-updater nvidia-apply' "$DISTRO/packages/cloudless-updater/cloudless-nvidia-apply.service"
grep -Fq 'DGX Spark detected; preserving' "$DISTRO/packages/cloudless-hardware/cloudless-hardware-install"
grep -Fq '745BF7A97F64EB716DAF7677974145C2D867C99E' "$DISTRO/scripts/install-dgx-spark.sh"
grep -Fq 'does not contain ARM64 yet' "$DISTRO/scripts/install-dgx-spark.sh"
grep -Fq 'Refusing an install plan that changes' "$DISTRO/scripts/install-dgx-spark.sh"
grep -Fq 'install -d -m 0755 /usr/share/keyrings' "$DISTRO/scripts/install-dgx-spark.sh"
! grep -Eq 'install -d -m 0700 .*usr/share/keyrings' "$DISTRO/scripts/install-dgx-spark.sh"
grep -Fq 'if ! is_dgx_spark' "$DISTRO/packages/cloudless-branding/postinst"
if grep -q '^Architectures:' "$DISTRO/packages/cloudless-updater/cloudless.sources"; then
    echo "Cloudless APT sources must follow the machine's native architecture" >&2
    exit 1
fi
bash "$DISTRO/scripts/test-package-content.sh"
if grep -Eq '^After=.*docker\.service' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"; then
    echo "cloudlessd must not delay the local UI behind Docker" >&2
    exit 1
fi

while IFS= read -r script; do bash -n "$script"; done < <(find "$DISTRO/scripts" "$DISTRO/packages" -type f \( -name '*.sh' -o -name postinst -o -name prerm -o -name cloudless-kiosk -o -name cloudless-dgx-desktop-mode -o -name cloudless-diagnostics -o -name cloudless-hardware-install -o -name cloudless-validate \))
echo "Package validation passed"
