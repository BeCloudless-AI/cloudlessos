#!/usr/bin/env bash
# Verify the model-detail backend wiring on the throwaway daemon (port 8799, no provision).
# Does NOT trigger any real download. Run from WSL:
#   bash /mnt/d/Cloudless/scripts/models-detail-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_ADDR="127.0.0.1:8799"
export CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"
P=8799
BIN=/tmp/cl-md
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-md 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-md.log 2>&1 &
trap 'pkill -x cl-md 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:$P/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "== /api/models =="
curl -s localhost:$P/api/models | python3 -c 'import json,sys;d=json.load(sys.stdin);print("gpu",d["gpuVRAMGB"],"yours",len(d["yours"]),"highlights",len(d["highlights"]),"current",d["current"])'

echo "== POST /api/models/download empty id -> expect 400 =="
curl -s -o /dev/null -w "%{http_code}\n" -X POST localhost:$P/api/models/download -d '{}'

echo "== POST /api/models/download bad json -> expect 400 =="
curl -s -o /dev/null -w "%{http_code}\n" -X POST localhost:$P/api/models/download -d 'garbage'

echo "DONE"
