#!/usr/bin/env bash
# Smoke-test the editable launch-command API on a throwaway daemon (no provisioning,
# so it falls back to the default engine — vLLM — for the command preview).
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state" CLOUDLESS_ADDR="127.0.0.1:8799" CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8789"
BIN=/tmp/cl-lc
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-lc 2>/dev/null || true; sleep 0.4
"$BIN" >/tmp/cl-lc.log 2>&1 &
trap 'pkill -x cl-lc 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done

M="Qwen/Qwen2.5-7B-Instruct"
show() { python3 -c '
import json,sys; d=json.load(sys.stdin)
print("  engine=%s model=%s overridden=%s"%(d["engine"],d["model"],d["overridden"]))
print("  prefix : "+d["prefix"][:88]+(" …" if len(d["prefix"])>88 else ""))
print("  command: "+d["command"])
'; }

echo "== GET default command for $M =="
curl -s "localhost:8799/api/engine/launch?model=$M" | show

echo "== POST an edited command (bump gpu-mem + max-len) =="
curl -s -X POST localhost:8799/api/engine/launch -H 'Content-Type: application/json' \
  -d "{\"model\":\"$M\",\"command\":\"$M --served-model-name cloudless --gpu-memory-utilization 0.85 --max-model-len 16384 --enable-auto-tool-choice --tool-call-parser hermes\"}" | show

echo "== GET again (should be overridden + persisted) =="
curl -s "localhost:8799/api/engine/launch?model=$M" | show
echo "  state.json engineCmds present:"; grep -q engineCmds "$CLOUDLESS_STATE_DIR/state.json" && echo "    yes" || echo "    NO"

echo "== POST a command identical to default (should drop the override) =="
DEF=$(curl -s "localhost:8799/api/engine/launch?model=${M}__never" >/dev/null; curl -s "localhost:8799/api/engine/launch?model=$M" | python3 -c 'import json,sys;print(json.load(sys.stdin)["defaultCommand"])')
curl -s -X POST localhost:8799/api/engine/launch -H 'Content-Type: application/json' -d "{\"model\":\"$M\",\"command\":\"$DEF\"}" | show

echo "== DELETE override (reset) =="
curl -s -X DELETE "localhost:8799/api/engine/launch?model=$M" | show
echo DONE
