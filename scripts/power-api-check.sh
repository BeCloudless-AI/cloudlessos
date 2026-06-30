#!/usr/bin/env bash
# Smoke-test /api/engine/power across ranges + the erase (DELETE) path, on a
# throwaway daemon (separate ports, no provisioning — won't touch the real one).
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state" CLOUDLESS_ADDR="127.0.0.1:8799" CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8789"
BIN=/tmp/cl-pw
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-pw 2>/dev/null || true; sleep 0.4
"$BIN" >/tmp/cl-pw.log 2>&1 &
trap 'pkill -x cl-pw 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "== /api/engine/power per range (bogus -> day) =="
for R in hour day month year bogus; do
  curl -s "localhost:8799/api/engine/power?range=$R" | python3 -c '
import json,sys; p=json.load(sys.stdin)
print("range=%-5s series=%2d totalWh=%.3f rangeWh=%.3f avgW=%.1f peakW=%.1f nowW=%.1f last=%r"%(
  p["range"], len(p["series"]), p["totalWh"], p["rangeWh"], p["avgW"], p["peakW"], p["nowW"], p.get("lastSampleAt")))
'
done

echo "== DELETE one day bucket (no data yet -> still 200, valid shape) =="
curl -s -X DELETE "localhost:8799/api/engine/power?range=day&key=2026-06-30" | python3 -c '
import json,sys; p=json.load(sys.stdin); print("after day-erase: range=%s series=%d totalWh=%.3f"%(p["range"],len(p["series"]),p["totalWh"]))'

echo "== DELETE all (clear log) =="
curl -s -X DELETE "localhost:8799/api/engine/power?range=year" | python3 -c '
import json,sys; p=json.load(sys.stdin); print("after clear-all: range=%s series=%d totalWh=%.3f"%(p["range"],len(p["series"]),p["totalWh"]))'
echo DONE
