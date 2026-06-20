#!/usr/bin/env bash
# Verify SGLang is pre-fetched (image ready, NOT running) and the catalog exposes
# the new apps (sglang service-hidden; openclaw/hermes as recipe-pending).
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/agents-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-ag
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-ag 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true   # free :8765 from any stale daemon
sg docker -c "$BIN" >/tmp/cl-ag.log 2>&1 &
trap 'pkill -x cl-ag 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT

for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done
sleep 5   # let the provisioner process bundled apps

echo "--- provision log ---"; grep -i 'provision' /tmp/cl-ag.log || true
echo "--- running cloudless-* containers (expect vllm/open-webui/comfyui, NOT sglang) ---"
sg docker -c "docker ps --format '{{.Names}}'" | grep '^cloudless-' || true
echo "--- sglang image present? ---"
sg docker -c "docker images lmsysorg/sglang:latest --format '{{.Repository}}:{{.Tag}} {{.Size}}'"
echo "--- catalog ---"
curl -s localhost:8765/api/catalog | tr '}' '\n' | grep -oE '"id":"[^"]+"|"service":(true|false)|"image":"[^"]*"' | paste - - - 2>/dev/null || curl -s localhost:8765/api/catalog
echo "AGENTS CHECK DONE"
