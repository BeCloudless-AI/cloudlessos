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
grep -Fq 'update-alternatives --set default.plymouth' \
    "$DISTRO/packages/cloudless-branding/postinst"
grep -Fq 'Wants=docker.service' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fq 'updates.becloudless.ai/apt' "$DISTRO/packages/cloudless-updater/cloudless.sources"
grep -Fq 'cloudless-updater apply' "$DISTRO/packages/cloudless-updater/cloudless-update-apply.service"
grep -Fq 'cloudless-updater check' "$DISTRO/packages/cloudless-updater/cloudless-update-check.service"
bash "$DISTRO/scripts/test-package-content.sh"
if grep -Eq '^After=.*docker\.service' "$DISTRO/packages/cloudless-orchestrator/cloudlessd.service"; then
    echo "cloudlessd must not delay the local UI behind Docker" >&2
    exit 1
fi

while IFS= read -r script; do bash -n "$script"; done < <(find "$DISTRO/scripts" "$DISTRO/packages" -type f \( -name '*.sh' -o -name postinst -o -name prerm -o -name cloudless-kiosk -o -name cloudless-hardware-install -o -name cloudless-validate \))
echo "Package validation passed"
