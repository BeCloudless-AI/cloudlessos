#!/usr/bin/env bash
# Verify /api/engine/metrics reports the ACTUAL active engine + accurate hint when
# metrics are off. The test daemon (NO_PROVISION) detects the real running engine
# container, so this reflects the live machine. Run from WSL.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"
export CLOUDLESS_ADDR="127.0.0.1:8799"
export CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8789"
BIN=/tmp/cl-em
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-em 2>/dev/null || true
sleep 0.4
sg docker -c "$BIN" >/tmp/cl-em.log 2>&1 &
trap 'pkill -x cl-em 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done
echo "== /api/engine (active) =="
curl -s localhost:8799/api/engine | python3 -c 'import json,sys;d=json.load(sys.stdin);print("active=%s ready=%s"%(d["active"],d["ready"]))'
echo "== /api/engine/metrics =="
curl -s localhost:8799/api/engine/metrics | python3 -c 'import json,sys;d=json.load(sys.stdin);print("available=%s engine=%s ready=%s canEnable=%s"%(d["available"],d["engine"],d.get("ready"),d.get("canEnable")));print("hint:",d.get("hint"))'
echo "DONE"
