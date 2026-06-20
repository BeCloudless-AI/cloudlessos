#!/usr/bin/env bash
# Verify the engine serves the full 32K context (fixes OpenClaw context overflow).
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/context-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-ctx
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-ctx 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
bash /mnt/d/Cloudless/scripts/reset-apps.sh
sg docker -c "$BIN" >/tmp/cl-ctx.log 2>&1 &
trap 'pkill -x cl-ctx 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "==> waiting for engine ready…"
for i in $(seq 1 120); do curl -s localhost:8765/api/engine | grep -q '"ready":true' && break; sleep 2; done
echo "engine        : $(curl -s localhost:8765/api/engine)"
echo "max_model_len : $(curl -s http://localhost:8000/v1/models | grep -o '"max_model_len":[0-9]*' | head -1)"
echo "--- engine startup (KV cache / context) ---"
sg docker -c "docker logs cloudless-vllm" 2>&1 | grep -iE "max_model_len|maximum concurrency|KV cache|GPU KV|Failed|ValueError" | tail -6 || true
echo "CONTEXT CHECK DONE"
