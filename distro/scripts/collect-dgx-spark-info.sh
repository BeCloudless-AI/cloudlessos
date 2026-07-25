#!/usr/bin/env bash
# Collect the non-sensitive hardware and platform facts needed to qualify
# CloudlessOS on DGX Spark. This script is read-only.
set -u

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
output="${1:-$PWD/cloudless-dgx-spark-report-$timestamp.tar.gz}"
work="$(mktemp -d)"
report="$work/system-report.txt"
trap 'rm -rf "$work"' EXIT

section() {
    printf '\n\n===== %s =====\n' "$1" >> "$report"
}

capture() {
    local title="$1"
    shift
    section "$title"
    printf '$' >> "$report"
    printf ' %q' "$@" >> "$report"
    printf '\n' >> "$report"
    if command -v "$1" >/dev/null 2>&1; then
        "$@" >> "$report" 2>&1 || printf '[command exited with status %s]\n' "$?" >> "$report"
    else
        printf '[command not installed]\n' >> "$report"
    fi
}

capture_file() {
    local title="$1" path="$2"
    section "$title"
    if [ -r "$path" ]; then
        tr '\0' '\n' < "$path" >> "$report"
    else
        printf '[not present or not readable]\n' >> "$report"
    fi
}

capture_dgx_release() {
    section "DGX release"
    if [ -r /etc/dgx-release ]; then
        grep -Evi '(SERIAL|UUID)' /etc/dgx-release >> "$report"
    else
        printf '[not present or not readable]\n' >> "$report"
    fi
}

printf 'CloudlessOS DGX Spark qualification report\n' > "$report"
printf 'Collected (UTC): %s\n' "$(date -u +%FT%TZ)" >> "$report"
printf 'Privacy: serial numbers, UUIDs, MAC/IP addresses, user data, logs, and repository configuration are intentionally excluded.\n' >> "$report"

capture "Kernel and architecture" uname -srvmo
capture "Debian architecture" dpkg --print-architecture
capture_file "Operating system" /etc/os-release
capture_dgx_release
capture_file "Device-tree model" /proc/device-tree/model
capture_file "Device-tree compatible identifiers" /proc/device-tree/compatible
capture_file "DMI product name" /sys/devices/virtual/dmi/id/product_name
capture_file "DMI product version" /sys/devices/virtual/dmi/id/product_version

capture "CPU topology" lscpu
capture "Memory summary" free -h
capture "NUMA topology" numactl --hardware
capture "PCI devices and drivers" lspci -nnk
capture "Block-device layout (identifiers excluded)" lsblk -e 7 -o NAME,TYPE,SIZE,FSTYPE,FSVER,LABEL,PARTLABEL,MOUNTPOINTS
capture "Mounted filesystems" findmnt -D -o SOURCE,FSTYPE,SIZE,USED,AVAIL,USE%,TARGET
capture "Secure Boot state" mokutil --sb-state
capture "Default boot target" systemctl get-default

capture "NVIDIA system management interface" nvidia-smi
capture "NVIDIA topology" nvidia-smi topo -m
capture "NVIDIA driver query" nvidia-smi --query-gpu=name,driver_version,memory.total,compute_cap --format=csv
if command -v nvcc >/dev/null 2>&1; then
    capture "CUDA compiler" nvcc --version
else
    capture "CUDA compiler" /usr/local/cuda/bin/nvcc --version
fi
capture "NVIDIA container toolkit" nvidia-ctk --version
capture "NVIDIA CDI devices" nvidia-ctk cdi list
capture "Docker version" docker version
capture "Docker runtimes" docker info --format "{{json .Runtimes}}"

capture "Relevant installed packages" dpkg-query -W -f '${binary:Package}\t${Version}\t${Architecture}\n' \
    'linux-*' 'nvidia-*' 'cuda-*' 'docker*' 'containerd*' 'libnvidia-container*'
capture "Relevant enabled services" systemctl list-unit-files \
    'docker*' 'containerd*' 'nvidia*' 'nv-*' 'cloud-init*' --no-pager
capture "Relevant active services" systemctl list-units \
    'docker*' 'containerd*' 'nvidia*' 'nv-*' 'cloud-init*' --all --no-pager

section "Network interfaces (addresses excluded)"
if command -v ip >/dev/null 2>&1; then
    while IFS= read -r interface; do
        printf '%s\n' "$interface" >> "$report"
        if command -v ethtool >/dev/null 2>&1; then
            ethtool -i "$interface" 2>&1 \
                | grep -E '^(driver|version|firmware-version|bus-info):' \
                | sed 's/^/  /' >> "$report" || true
        fi
    done < <(find /sys/class/net -mindepth 1 -maxdepth 1 -printf '%f\n' 2>/dev/null | sort)
else
    printf '[ip command not installed]\n' >> "$report"
fi

mkdir -p "$(dirname "$output")"
tar -czf "$output" -C "$work" system-report.txt
printf 'Created: %s\n' "$output"
printf 'You can inspect it with: tar -xOf %q system-report.txt | less\n' "$output"
