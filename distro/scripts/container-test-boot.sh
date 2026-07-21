#!/usr/bin/env bash
set -euo pipefail
if [ "$(id -u)" -ne 0 ]; then
    echo "container-test-boot.sh must run as root inside the test container" >&2
    exit 1
fi
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq imagemagick ovmf qemu-system-x86 socat >/dev/null
bash distro/scripts/test-boot.sh
