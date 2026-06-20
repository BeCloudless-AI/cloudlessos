#!/usr/bin/env bash
# Rebuild, recreate the engine with tool-calling enabled, and verify a tool
# request is accepted. Run from WSL Ubuntu: bash /mnt/d/Cloudless/scripts/tool-verify.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-tv
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-tv 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
bash /mnt/d/Cloudless/scripts/reset-apps.sh
sg docker -c "$BIN" >/tmp/cl-tv.log 2>&1 &
trap 'pkill -x cl-tv 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "==> waiting for vLLM (with tool calling) to be ready…"
for i in $(seq 1 120); do curl -s localhost:8765/api/engine | grep -q '"ready":true' && break; sleep 2; done
echo "engine: $(curl -s localhost:8765/api/engine)"
echo
bash /mnt/d/Cloudless/scripts/tool-check.sh
echo "TOOL VERIFY DONE"
