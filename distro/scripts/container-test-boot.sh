#!/usr/bin/env bash
set -euo pipefail
if [ "$(id -u)" -ne 0 ]; then
    echo "container-test-boot.sh must run as root inside the test container" >&2
    exit 1
fi
missing=false
for command in convert qemu-system-x86_64 socat xorriso; do
    command -v "$command" >/dev/null 2>&1 || missing=true
done
if $missing; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq imagemagick ovmf qemu-system-x86 socat xorriso >/dev/null
fi
bash distro/scripts/test-boot.sh
