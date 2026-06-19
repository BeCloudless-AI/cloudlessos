#!/usr/bin/env bash
#
# Install the Go toolchain into /usr/local/go on WSL2 Ubuntu (dev environment).
# Pinned version; bump GO_VERSION to upgrade.
#
# Usage (from WSL Ubuntu, sudo credentials primed or will prompt):
#     bash /mnt/d/Cloudless/scripts/install-go.sh

set -euo pipefail

GO_VERSION="${GO_VERSION:-go1.26.4}"
TARBALL="${GO_VERSION}.linux-amd64.tar.gz"
URL="https://go.dev/dl/${TARBALL}"

echo "==> Downloading ${GO_VERSION} from ${URL}"
cd /tmp
curl -fSL -o "${TARBALL}" "${URL}"

echo "==> Installing to /usr/local/go"
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "${TARBALL}"
rm -f "${TARBALL}"

echo "==> Persisting PATH in ~/.bashrc"
if ! grep -q "/usr/local/go/bin" "${HOME}/.bashrc"; then
  echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> "${HOME}/.bashrc"
fi

export PATH="$PATH:/usr/local/go/bin"
echo "==> Installed: $(go version)"
