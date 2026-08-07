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
    package_contents="$(dpkg-deb --contents "$package")"
    package_name="$(dpkg-deb -f "$package" Package)"
    grep -Fq "./usr/share/doc/$package_name/LICENSE" <<<"$package_contents"
    grep -Fq "./usr/share/doc/$package_name/NOTICE" <<<"$package_contents"
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
grep -Fq '$HOME/snap/firefox/common/cloudless-kiosk' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'browser.sessionstore.resume_from_crash", false' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'nvidia-smi -L' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'gfx.canvas.accelerated", true' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'gfx.webrender.all", true' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'layers.acceleration.disabled", false' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'MOZ_WEBRENDER=1' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'gfx.canvas.accelerated", false' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'gfx.webrender.software", true' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'LIBGL_ALWAYS_SOFTWARE=1 MOZ_WEBRENDER=0' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'DGX Spark detected; using Firefox software rendering' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'firefox_profile_running "$PROFILE_DIR"' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'recovered Firefox already owns the kiosk profile' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq -- '--no-remote --profile "$PROFILE_DIR"' \
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
grep -Fq 'flock -w "$LOCK_WAIT_SECONDS" 9' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq '9>&-' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
grep -Fq -- '--class CloudlessKiosk' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'file:///usr/share/cloudless/recovery.html' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'sudo cloudless-repair --repair' \
    "$DISTRO/packages/cloudless-shell/recovery.html"
grep -Fq '/usr/bin/cloudless-browser-agent' \
    "$DISTRO/packages/cloudless-shell/openbox-autostart"
grep -Fq '/usr/bin/cloudless-display-watch &' \
    "$DISTRO/packages/cloudless-shell/openbox-autostart"
grep -Fq '/api/system/display/normalize' \
    "$DISTRO/packages/cloudless-shell/cloudless-kiosk"
grep -Fq 'systemctl restart --no-block lightdm.service' \
    "$DISTRO/packages/cloudless-shell/postinst"
grep -Fq 'CLOUDLESS_UPDATE_TRANSACTION' \
    "$DISTRO/packages/cloudless-shell/postinst"
grep -Fq 'pkill -f' \
    "$DISTRO/packages/cloudless-shell/cloudless-display-watch"
grep -Fq 'nvidia-smi -L' \
    "$DISTRO/packages/cloudless-shell/cloudless-wait-for-display"
grep -Fq 'connected_display_ready' \
    "$DISTRO/packages/cloudless-shell/cloudless-wait-for-display"
grep -Fq 'ExecStartPre=/usr/lib/cloudless/cloudless-wait-for-display' \
    "$DISTRO/packages/cloudless-shell/lightdm-wait-display.conf"
grep -Fq '/run/cloudless-browser/requests' \
    "$DISTRO/packages/cloudless-shell/cloudless-browser.tmpfiles"
sh -n "$DISTRO/packages/cloudless-shell/cloudless-browser-agent"
sh -n "$DISTRO/packages/cloudless-shell/cloudless-wait-for-display"
sh -n "$DISTRO/packages/cloudless-orchestrator/cloudless-install-tailscale"
bash "$DISTRO/scripts/test-tailscale-installer.sh"
bash -n "$DISTRO/packages/cloudless-orchestrator/cloudless-backup"
tailscale_installer="$DISTRO/packages/cloudless-orchestrator/cloudless-install-tailscale"
grep -Fq '2596A99EAAB33821893C0A79458CA832957F5868' "$tailscale_installer"
grep -Fq '2F625B3A774B946822EDDBEEB1547A3DDAAF03C6' "$tailscale_installer"
grep -Fq 'https://pkgs.tailscale.com/stable/ubuntu/noble.noarmor.gpg' "$tailscale_installer"
grep -Fq 'signed-by=' "$tailscale_installer"
grep -Fq 'Dir::Etc::sourcelist=' "$tailscale_installer"
grep -Fq -- '--homedir "$GNUPGHOME"' "$tailscale_installer"
if grep -Eq 'tailscale\.com/install\.sh|(^|[[:space:]])sh[[:space:]]+["$]' "$tailscale_installer"; then
    echo "Tailscale installation must use its fingerprint-pinned APT repository, not a remote shell" >&2
    exit 1
fi
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
firstboot_deb="$(find "$OUT" -maxdepth 1 -type f -name 'cloudless-firstboot_*_amd64.deb' -print -quit)"
test -n "$firstboot_deb"
dpkg-deb -f "$orchestrator_deb" Depends | grep -Eq '(^|, )gpgv(,|$)'
dpkg-deb -f "$orchestrator_deb" Depends | grep -Eq '(^|, )gnupg(,|$)'
dpkg-deb -f "$orchestrator_deb" Depends | grep -Eq '(^|, )python3(,|$)'
dpkg-deb -f "$orchestrator_deb" Depends | grep -Eq '(^|, )util-linux(,|$)'
dpkg-deb -f "$orchestrator_deb" Depends | grep -Eq '(^|, )nfs-common(,|$)'
dpkg-deb -f "$orchestrator_deb" Depends | grep -Eq '(^|, )nfs-kernel-server(,|$)'
dpkg-deb -f "$shell_deb" Depends | grep -Eq '(^|, )xdotool(,|$)'
dpkg-deb -c "$orchestrator_deb" |
    grep -F './usr/share/cloudless/cloudless-archive-keyring.pgp' >/dev/null
dpkg-deb -c "$orchestrator_deb" |
    grep -F './usr/share/cloudless/community-keys.json' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './usr/lib/cloudless/cloudless-install-tailscale' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './usr/lib/cloudless/cloudless-privileged' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './usr/lib/cloudless/cloudless-engine' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './usr/lib/cloudless/cloudless-model-storage' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './usr/lib/cloudless/cloudless-docker' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './usr/sbin/cloudless-backup' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './lib/systemd/system/cloudless-tailscale-install.service' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './lib/systemd/system/cloudless-privileged.service' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './lib/systemd/system/cloudless-engine.service' >/dev/null
dpkg-deb -c "$orchestrator_deb" | grep -F './lib/systemd/system/cloudless-model-storage.service' >/dev/null
dpkg-deb -c "$shell_deb" | grep -F './usr/bin/cloudless-browser-agent' >/dev/null
dpkg-deb -c "$shell_deb" | grep -F './usr/bin/cloudless-desktop-agent' >/dev/null
dpkg-deb -c "$shell_deb" | grep -F './usr/lib/tmpfiles.d/cloudless-browser.conf' >/dev/null
dpkg-deb -c "$shell_deb" | grep -F './usr/lib/tmpfiles.d/cloudless-desktop.conf' >/dev/null
dpkg-deb -c "$shell_deb" | grep -F './usr/share/cloudless/recovery.html' >/dev/null
dpkg-deb -c "$firstboot_deb" | grep -F './usr/bin/cloudless-repair' >/dev/null
dpkg-deb -c "$firstboot_deb" | grep -F './usr/bin/cloudless-qualify' >/dev/null
dpkg-deb -c "$firstboot_deb" |
    grep -F './usr/share/cloudless/physical-validation-matrix.json' >/dev/null
dpkg-deb -f "$firstboot_deb" Depends | grep -Eq '(^|, )python3(,|$)'
dpkg-deb -c "$firstboot_deb" | grep -F './usr/lib/cloudless/cloudless-boot-audit' >/dev/null
dpkg-deb -c "$firstboot_deb" | grep -F './usr/lib/cloudless/cloudless-graphical-recovery' >/dev/null
dpkg-deb -c "$firstboot_deb" | grep -F './lib/systemd/system/cloudless-graphical-recovery.service' >/dev/null
dpkg-deb -c "$firstboot_deb" | grep -F './lib/systemd/system/cloudless-qualification-boot.service' >/dev/null
grep -Fq 'ExecStart=/usr/lib/cloudless/cloudless-boot-audit' \
    "$DISTRO/packages/cloudless-firstboot/cloudless-firstboot.service"
grep -Fq 'OnFailure=cloudless-graphical-recovery.service' \
    "$DISTRO/packages/cloudless-firstboot/cloudless-firstboot.service"
grep -Fq 'WantedBy=graphical.target' \
    "$DISTRO/packages/cloudless-firstboot/cloudless-firstboot.service"
if grep -Eq '^(After|Wants)=.*graphical\.target' \
    "$DISTRO/packages/cloudless-firstboot/cloudless-firstboot.service"; then
    echo "cloudless-firstboot must not wait on the target that starts it" >&2
    exit 1
fi
grep -Fq 'systemctl reenable cloudless-firstboot.service' \
    "$DISTRO/packages/cloudless-firstboot/postinst"
grep -Fq 'ExecStart=/usr/bin/cloudless-qualify boot-active' \
    "$DISTRO/packages/cloudless-firstboot/cloudless-qualification-boot.service"
grep -Fq 'Requires=cloudless-firstboot.service' \
    "$DISTRO/packages/cloudless-firstboot/cloudless-qualification-boot.service"
grep -Fq 'WantedBy=graphical.target' \
    "$DISTRO/packages/cloudless-firstboot/cloudless-qualification-boot.service"
grep -Fq 'systemctl reenable cloudless-qualification-boot.service' \
    "$DISTRO/packages/cloudless-firstboot/postinst"
grep -Fq 'cloudless-graphical-recovery-' \
    "$DISTRO/packages/cloudless-firstboot/cloudless-graphical-recovery"
restart_line="$(grep -n 'systemctl restart lightdm.service' "$DISTRO/packages/cloudless-firstboot/cloudless-repair" | head -n1 | cut -d: -f1)"
check_line="$(grep -n '^check_service docker.service' "$DISTRO/packages/cloudless-firstboot/cloudless-repair" | head -n1 | cut -d: -f1)"
test -n "$restart_line" && test -n "$check_line" && test "$restart_line" -lt "$check_line" || {
    echo "cloudless-repair must restart LightDM before evaluating the recovered display" >&2
    exit 1
}
grep -Fq 'update-alternatives --set default.plymouth' \
    "$DISTRO/packages/cloudless-branding/postinst"
grep -Fq 'Requires=cloudless-engine.service' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fqx 'User=cloudlessd' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fqx 'Group=cloudless-control' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fqx 'SupplementaryGroups=cloudless' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fqx 'AmbientCapabilities=CAP_NET_RAW' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fqx 'CapabilityBoundingSet=CAP_NET_RAW' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fq 'chown -R cloudlessd:cloudless-control' "$DISTRO/packages/cloudless-orchestrator/postinst"
if grep -Fq '/run/docker.sock' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"; then
    echo "cloudlessd must not retain Docker socket access after engine delegation" >&2
    exit 1
fi
grep -Fq 'CacheDirectory=cloudless' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fq '.cloudless-shared-storage-v1' "$DISTRO/packages/cloudless-orchestrator/postinst"
grep -Fq 'systemctl disable --now cloudless-model-storage.service' \
    "$DISTRO/packages/cloudless-orchestrator/postinst"
grep -Fq 'systemctl reset-failed cloudless-model-storage.service' \
    "$DISTRO/packages/cloudless-orchestrator/postinst"
grep -Fq 'test -d /var/lib/cloudless/models-cache && test -w /var/lib/cloudless/models-cache' \
    "$DISTRO/packages/cloudless-orchestrator/postinst"
for netplan_runtime_path in /etc/netplan /run/systemd/system /run/systemd/network /run/NetworkManager/conf.d /run/NetworkManager/system-connections /run/udev; do
    if grep -Fq "$netplan_runtime_path" "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"; then
        echo "cloudlessd must not retain delegated Netplan write access: $netplan_runtime_path" >&2
        exit 1
    fi
    grep -Fq "$netplan_runtime_path" "$DISTRO/packages/cloudless-orchestrator/cloudless-privileged.service" || {
        echo "privileged broker sandbox blocks required Netplan runtime path: $netplan_runtime_path" >&2
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

bash "$DISTRO/scripts/test-boot-audit.sh"
bash "$DISTRO/scripts/test-qualification-contract.sh"
bash "$DISTRO/scripts/test-service-hardening.sh"
bash "$DISTRO/scripts/test-browser-agent.sh"
bash "$DISTRO/scripts/test-kiosk.sh"
bash -n "$DISTRO/scripts/test-installed-session.sh"
while IFS= read -r script; do bash -n "$script"; done < <(find "$DISTRO/scripts" "$DISTRO/packages" -type f \( -name '*.sh' -o -name postinst -o -name prerm -o -name cloudless-kiosk -o -name cloudless-dgx-desktop-mode -o -name cloudless-diagnostics -o -name cloudless-repair -o -name cloudless-boot-audit -o -name cloudless-graphical-recovery -o -name cloudless-hardware-install -o -name cloudless-validate \))
echo "Package validation passed"
