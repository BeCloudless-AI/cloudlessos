#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
OUT="${CLOUDLESS_PACKAGE_OUT:-$DISTRO/out/packages}"
if [ "${CLOUDLESS_SKIP_PACKAGE_BUILD:-0}" != "1" ]; then
    "$DISTRO/scripts/build-packages.sh"
fi
for package in "$OUT"/*.deb; do
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
grep -Fq '$HOME/snap/chromium/common/cloudless-web-browser' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq '$HOME/snap/firefox/common/cloudless-web-browser' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq -- '--new-tab "$url"' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq -- '--class CloudlessBrowser' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq 'wmctrl -i -r "$window" -b add,above' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq 'wmctrl -i -r "$KIOSK_WINDOW" -b add,below' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq 'xdotool windowraise "$window"' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq 'wmctrl -i -a "$window"' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq 'flock -n 9' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq '9>&-' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq -- '--class CloudlessKiosk' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq '/usr/bin/cloudless-browser-agent' \
    "$DISTRO/packages/cloudless-shell/openbox-autostart"
grep -Fq '/run/cloudless-browser/requests' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser.tmpfiles"
sh -n "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
sh -n "$DISTRO/packages/cloudless-orchestrator/cloudless-install-tailscale"
grep -Fq 'https://tailscale.com/install.sh' \
    "$DISTRO/packages/cloudless-orchestrator/cloudless-install-tailscale"
grep -Fq 'cloudless-tailscale-install.service' "$DISTRO/scripts/build-packages.sh"
grep -Fq 'Restart=on-failure' \
    "$DISTRO/packages/cloudless-orchestrator/cloudless-tailscale-install.service"
grep -Fq '/usr/bin/ttyd' "$DISTRO/packages/cloudless-shell/cloudless-terminal.service"
grep -Fq 'disable --now ttyd.service' "$DISTRO/packages/cloudless-shell/postinst"
grep -Fq 'export CUDA_HOME=/usr/local/cuda' "$DISTRO/packages/cloudless-shell/cloudless-cuda.sh"
grep -Fq 'ninja-build' "$DISTRO/packages/cloudless-shell/cloudless-developer-tools"
grep -Fq 'startup.png' "$DISTRO/packages/cloudless-shell/openbox-autostart"
grep -Fq 'pcmanfm' "$DISTRO/scripts/build-packages.sh"
grep -Fq 'CLOUDLESS_HOME=/home/cloudless/Cloudless' \
    "$DISTRO/packages/cloudless-orchestrator/cloudless.env"
grep -Fq 'CLOUDLESS_DESKTOP_HOME=/home/cloudless' \
    "$DISTRO/packages/cloudless-orchestrator/cloudless.env"
grep -Fq 'capabilities.BuildVersion=$VERSION' "$DISTRO/scripts/build-packages.sh"
orchestrator_deb="$(find "$OUT" -maxdepth 1 -type f -name 'cloudless-orchestrator_*_amd64.deb' -print -quit)"
test -n "$orchestrator_deb"
shell_deb="$(find "$OUT" -maxdepth 1 -type f -name 'cloudless-shell_*_amd64.deb' -print -quit)"
test -n "$shell_deb"
dpkg-deb -f "$orchestrator_deb" Depends | grep -Eq '(^|, )gpgv(,|$)'
dpkg-deb -f "$orchestrator_deb" Depends | grep -Eq '(^|, )xdotool(,|$)'
dpkg-deb -c "$orchestrator_deb" |
    grep -F './usr/share/cloudless/cloudless-archive-keyring.pgp' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './usr/lib/cloudless/cloudless-install-tailscale' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './lib/systemd/system/cloudless-tailscale-install.service' >/dev/null
dpkg-deb -c "$shell_deb" | grep -F './usr/bin/cloudless-browser-agent' >/dev/null
dpkg-deb -c "$shell_deb" | grep -F './usr/lib/tmpfiles.d/cloudless-browser.conf' >/dev/null
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
