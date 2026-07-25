#!/bin/bash
# Install CloudlessOS as a signed, reversible appliance layer on NVIDIA DGX OS.
set -euo pipefail

APT_BASE="${CLOUDLESS_APT_BASE_URL:-https://updates.becloudless.ai/apt}"
CHANNEL="${CLOUDLESS_CHANNEL:-stable}"
EXPECTED_FINGERPRINT="745BF7A97F64EB716DAF7677974145C2D867C99E"
MODE=cloudless
ASSUME_YES=false
CHECK_ONLY=false
PACKAGES=(
    cloudless-orchestrator cloudless-shell cloudless-branding
    cloudless-hardware cloudless-firstboot cloudless-updater
)

usage() {
    cat <<'EOF'
Usage: sudo bash install-dgx-spark.sh [--side-by-side] [--check] [--yes]

  (default)       Boot directly into the CloudlessOS appliance interface.
  --side-by-side  Keep NVIDIA's GNOME login; Cloudless runs at localhost:8765.
  --check         Run all hardware and signed-repository preflight checks only.
  --yes           Skip the confirmation prompt.
EOF
}
for arg in "$@"; do
    case "$arg" in
        --side-by-side) MODE=side-by-side ;;
        --check) CHECK_ONLY=true ;;
        --yes|-y) ASSUME_YES=true ;;
        --help|-h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done

[ "$(id -u)" -eq 0 ] || { echo "Run this installer with sudo." >&2; exit 1; }
[ "$(dpkg --print-architecture)" = arm64 ] || {
    echo "This installer is only for the ARM64 NVIDIA DGX Spark." >&2; exit 1;
}

is_spark=false
if [ -r /etc/dgx-release ]; then is_spark=true; fi
if [ -r /sys/devices/virtual/dmi/id/product_name ] &&
   grep -Eqi 'DGX[[:space:]_-]*Spark|GB10' /sys/devices/virtual/dmi/id/product_name; then
    is_spark=true
fi
$is_spark || { echo "DGX Spark was not detected. No changes were made." >&2; exit 1; }

for command in curl gpg gpgv nvidia-smi nvidia-ctk docker; do
    command -v "$command" >/dev/null || {
        echo "Required DGX OS component is missing: $command. Update/repair DGX OS first." >&2
        exit 1
    }
done
nvidia-smi >/dev/null || { echo "The NVIDIA driver is not ready. No changes were made." >&2; exit 1; }
docker info >/dev/null || { echo "Docker is not ready. No changes were made." >&2; exit 1; }
nvidia-ctk cdi list 2>/dev/null | grep -q 'nvidia.com/gpu=all' || {
    echo "The DGX CDI GPU device is unavailable. No changes were made." >&2; exit 1;
}

echo "DGX Spark detected."
echo "DGX OS: $(tr '\n' ' ' < /etc/dgx-release 2>/dev/null || echo unknown)"
echo "Driver: $(nvidia-smi --query-gpu=driver_version --format=csv,noheader | head -n1)"
echo "Install mode: $MODE"
if ! $CHECK_ONLY && ! $ASSUME_YES; then
    read -r -p "Install the CloudlessOS layer without replacing DGX OS? [y/N] " answer
    case "$answer" in y|Y|yes|YES) ;; *) echo "Cancelled."; exit 0 ;; esac
fi

key_tmp="$(mktemp)"
release_tmp="$(mktemp)"
trap 'rm -f "$key_tmp" "$release_tmp"' EXIT
curl -fsSL "$APT_BASE/cloudless-archive-keyring.pgp" -o "$key_tmp"
curl -fsSL "$APT_BASE/dists/$CHANNEL/InRelease" -o "$release_tmp"
actual_fingerprint="$(gpg --batch --show-keys --with-colons "$key_tmp" |
    awk -F: '$1 == "fpr" {print $10; exit}')"
[ "$actual_fingerprint" = "$EXPECTED_FINGERPRINT" ] || {
    echo "The Cloudless archive key fingerprint is not trusted. No packages were installed." >&2
    exit 1
}
gpgv --keyring "$key_tmp" "$release_tmp" >/dev/null
grep -Eq '^Architectures:.*[[:space:]]arm64([[:space:]]|$)' "$release_tmp" || {
    echo "The signed $CHANNEL repository does not contain ARM64 yet. No packages were installed." >&2
    exit 1
}
if $CHECK_ONLY; then
    echo "Preflight passed: hardware, CDI, archive fingerprint, signature, and ARM64 repository."
    exit 0
fi

backup="/var/lib/cloudless/dgx-backup/$(date -u +%Y%m%dT%H%M%SZ)"
install -d -m 0700 "$backup" /etc/cloudless
# APT downloads repository metadata as the unprivileged _apt user. Keep the
# shared keyring directory traversable so every configured Signed-By key,
# including NVIDIA's DGX Spark key, remains readable.
install -d -m 0755 /usr/share/keyrings
cp -a /etc/dgx-release "$backup/" 2>/dev/null || true
cp -a /etc/os-release "$backup/etc-os-release" 2>/dev/null || true
cp -a /usr/lib/os-release "$backup/usr-lib-os-release" 2>/dev/null || true
cp -a /etc/X11/default-display-manager "$backup/" 2>/dev/null || true
readlink -f /etc/systemd/system/display-manager.service > "$backup/display-manager.txt" 2>/dev/null || true
readlink -f /etc/systemd/system/default.target > "$backup/default-target.txt" 2>/dev/null || true
dpkg-query -W -f='${binary:Package}\t${Version}\n' \
    'nvidia-*' 'libnvidia-*' '*nvidia*' 'cuda-*' 'linux-*nvidia*' 'dgx-*' \
    > "$backup/nvidia-packages.tsv" 2>/dev/null || true

install -m 0644 "$key_tmp" /usr/share/keyrings/cloudless-archive-keyring.pgp
cat > /etc/apt/sources.list.d/cloudless.sources <<EOF
Enabled: yes
Types: deb
URIs: $APT_BASE
Suites: $CHANNEL
Components: main
Architectures: arm64
Signed-By: /usr/share/keyrings/cloudless-archive-keyring.pgp
EOF

if [ "$MODE" = cloudless ]; then
    : > /etc/cloudless/dgx-appliance
else
    rm -f /etc/cloudless/dgx-appliance
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update
simulation="$(apt-get -s install "${PACKAGES[@]}")"
protected_plan="$(printf '%s\n' "$simulation" | awk '
    $1 == "Inst" || $1 == "Remv" || $1 == "Purg" {
        name=tolower($2)
        sub(/:.*/, "", name)
        if (name ~ /nvidia/ || name ~ /^cuda-/ || name ~ /^dgx-/) print $0
    }
')"
if [ -n "$protected_plan" ]; then
    printf '%s\n' "$protected_plan"
    echo "Refusing an install plan that changes the NVIDIA-managed DGX OS stack." >&2
    exit 1
fi
apt-get install -y "${PACKAGES[@]}"

systemctl enable --now docker.service cloudlessd.service
systemctl enable cloudless-hardware.service cloudless-firstboot.service \
    cloudless-update-check.timer cloudless-nvidia-check.timer
if [ "$MODE" = cloudless ]; then
    cloudless-dgx-desktop-mode cloudless
else
    cloudless-dgx-desktop-mode dgx
fi

echo
echo "CloudlessOS was installed over DGX OS without replacing NVIDIA's kernel,"
echo "driver, CUDA, firmware, Docker, CDI configuration, or Secure Boot chain."
echo "Recovery metadata: $backup"
if [ "$MODE" = cloudless ]; then
    echo "Reboot when ready. To restore NVIDIA's desktop later:"
    echo "  sudo cloudless-dgx-desktop-mode dgx"
else
    echo "Open http://127.0.0.1:8765 after cloudlessd finishes starting."
fi
