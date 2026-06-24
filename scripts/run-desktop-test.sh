#!/usr/bin/env bash
# Build + run the daemon on the throwaway port (8799, no provision) for visual/UI testing.
# Run from WSL:  bash /mnt/d/Cloudless/scripts/run-desktop-test.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_ADDR="127.0.0.1:8799"
export CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"
cd /mnt/d/Cloudless/orchestrator && go build -o /tmp/cl-bld ./cmd/cloudlessd || exit 1
pkill -x cl-bld 2>/dev/null || true
sleep 0.5
sg docker -c /tmp/cl-bld >/tmp/cl-bld.log 2>&1 &
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done
echo "HEALTH: $(curl -s localhost:8799/api/health)"
echo "APPS: $(curl -s localhost:8799/api/apps | head -c 120)"
