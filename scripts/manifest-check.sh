#!/usr/bin/env bash
# Verify the daemon consumes the hosted Cloudless manifest: the update check should
# report source=cloudless and compare the installed image against the pinned digest.
# Uses the DEFAULT manifest URL (samuelcardillo.com). Reads the shared docker daemon,
# so it sees the already-installed images. Run from WSL Ubuntu.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_ADDR="127.0.0.1:8799"
export CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"
P=8799
BIN=/tmp/cl-mf
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-mf 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-mf.log 2>&1 &
trap 'pkill -x cl-mf 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:$P/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "manifest URL used : $(grep -o 'manifest: [^ ]*' /tmp/cl-mf.log | head -1)"
echo "open-webui update : $(curl -s localhost:$P/api/apps/open-webui/update)"
echo "comfyui update    : $(curl -s localhost:$P/api/apps/comfyui/update)"
echo "MANIFEST CHECK DONE"
