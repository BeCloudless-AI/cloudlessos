#!/usr/bin/env bash
# Build the complete installer from the official Go container on Docker-only hosts.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
    echo "container-build-iso.sh must run as root inside the build container" >&2
    exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl imagemagick librsvg2-bin xorriso >/dev/null
bash distro/scripts/build-iso.sh
bash distro/scripts/test-iso.sh
