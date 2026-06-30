#!/usr/bin/env bash
# Verify /api/engine/usage shape across all ranges + gateway-recorded success rate,
# on the throwaway daemon.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state" CLOUDLESS_ADDR="127.0.0.1:8799" CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8789"
BIN=/tmp/cl-us
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-us 2>/dev/null || true; sleep 0.4
sg docker -c "$BIN" >/tmp/cl-us.log 2>&1 &
trap 'pkill -x cl-us 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done
echo "== generate a key + 3 gateway requests (no engine -> 502 -> failures) =="
KEY=$(curl -s -X POST localhost:8799/api/keys -H 'Content-Type: application/json' -d '{"name":"t"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["key"])')
for i in 1 2 3; do curl -s -o /dev/null -H "Authorization: Bearer $KEY" localhost:8789/v1/models; done
echo "== /api/engine/usage per range (bogus -> falls back to day) =="
for R in hour day month year bogus; do
  curl -s "localhost:8799/api/engine/usage?range=$R" | python3 -c '
import json,sys; u=json.load(sys.stdin)
print("range=%-5s series=%2d peak=%s/%r succ=%s api=%s last=%r"%(
  u["range"], len(u["series"]), u["peakRequests"], u.get("peakLabel"),
  u["successRate"], u["apiRequests"], u.get("lastRequestAt")))
'
done
echo DONE
