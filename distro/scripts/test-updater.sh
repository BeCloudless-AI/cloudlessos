#!/usr/bin/env bash
set -euo pipefail
REPO="${1:-/repo}"
KEY="${2:-/test-key.pgp}"
PACKAGES="${3:-/src/distro/out/packages}"

test -s "$REPO/dists/stable/InRelease"
test -s "$KEY"
dpkg --unpack "$PACKAGES"/cloudless-*.deb >/dev/null
install -Dm0644 "$KEY" /usr/share/keyrings/cloudless-archive-keyring.pgp
sed -i \
    -e 's/^Enabled: no$/Enabled: yes/' \
    -e "s#^URIs:.*#URIs: file:$REPO#" \
    /etc/apt/sources.list.d/cloudless.sources
CLOUDLESS_UPDATE_STATUS=/tmp/cloudless-update-status.json \
    /usr/lib/cloudless/cloudless-updater check
grep -q '"state": "available"' /tmp/cloudless-update-status.json
grep -q '"availableVersion": "0.1.1"' /tmp/cloudless-update-status.json
test "$(grep -c '"name": "cloudless-' /tmp/cloudless-update-status.json)" -eq 6
echo "Updater integration validation passed"
