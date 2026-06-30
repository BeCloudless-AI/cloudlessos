#!/usr/bin/env bash
# Measure per-endpoint latency after the no-sleep sysload + cached GPU changes.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state" CLOUDLESS_ADDR="127.0.0.1:8799" CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8789"
BIN=/tmp/cl-lat
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-lat 2>/dev/null || true; sleep 0.4
sg docker -c "$BIN" >/tmp/cl-lat.log 2>&1 &
trap 'pkill -x cl-lat 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done

ms() { curl -s -o /dev/null -w "%{time_total}" "localhost:8799$1" | awk '{printf "%4.0f ms", $1*1000}'; }

echo "== /api/sysload (was a blocking 120ms sleep; should now be ~instant) =="
for i in 1 2 3; do echo "  call $i: $(ms /api/sysload)"; done

echo "== /api/gpu back-to-back (2nd/3rd within 700ms TTL should hit cache) =="
for i in 1 2 3; do echo "  call $i: $(ms /api/gpu)"; done

echo "== 5 concurrent /api/gpu (single-flight: one nvidia-smi, others share) =="
for i in $(seq 1 5); do (curl -s -o /dev/null -w "  concurrent: %{time_total}s\n" localhost:8799/api/gpu &) ; done; sleep 2
echo DONE