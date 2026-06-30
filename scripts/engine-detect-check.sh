#!/usr/bin/env bash
# Confirm the rewritten single-List activeEngine still detects the running engine.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state" CLOUDLESS_ADDR="127.0.0.1:8799" CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8789"
BIN=/tmp/cl-eng
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-eng 2>/dev/null || true; sleep 0.4
sg docker -c "$BIN" >/tmp/cl-eng.log 2>&1 &
trap 'pkill -x cl-eng 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done
echo "=== /api/engine (rewritten single-List activeEngine) ==="
curl -s localhost:8799/api/engine | python3 -m json.tool
