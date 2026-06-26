#!/usr/bin/env bash
# Verify the 3 engines are listed and /api/sysload returns live CPU/RAM.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state" CLOUDLESS_ADDR="127.0.0.1:8799" CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8789"
BIN=/tmp/cl-es
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-es 2>/dev/null || true; sleep 0.4
sg docker -c "$BIN" >/tmp/cl-es.log 2>&1 &
trap 'pkill -x cl-es 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done
echo "== /api/engine engines =="
curl -s localhost:8799/api/engine | python3 -c 'import json,sys;d=json.load(sys.stdin);print("active:",d["active"]);print("engines:",[(e["id"],e["name"],e["active"]) for e in d["engines"]])'
echo "== /api/sysload =="
curl -s localhost:8799/api/sysload | python3 -c 'import json,sys;d=json.load(sys.stdin);print("cpu=%s cores=%s cpuPct=%.1f ram=%d/%d MB"%(d["cpuModel"],d["cores"],d["cpuPct"],d["memUsedMB"],d["memTotalMB"]))'
echo DONE
