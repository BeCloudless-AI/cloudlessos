#!/usr/bin/env bash
# Full persistence + single-engine invariant: switch to SGLang, restart the
# daemon (must keep SGLang, no dual engine, no port conflict), switch back to vLLM.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/engine-persist-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-ps
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-ps 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
trap 'pkill -x cl-ps 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT

start_daemon() {
  sg docker -c "$BIN" >/tmp/cl-ps.log 2>&1 &
  for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && return; sleep 0.5; done
}
wait_engine() { # $1 = engine id
  for i in $(seq 1 120); do
    r=$(curl -s localhost:8765/api/engine)
    if echo "$r" | grep -q "\"active\":\"$1\"" && echo "$r" | grep -q '"ready":true'; then return; fi
    sleep 2
  done
}
engines_running() { sg docker -c "docker ps --format '{{.Names}}'" | grep -E '^cloudless-(vllm|sglang)$' | tr '\n' ' '; echo; }

echo "== boot 1 =="; start_daemon; sleep 4
echo "engine: $(curl -s localhost:8765/api/engine)"

echo "== switch to SGLang (persists choice) =="
curl -s -X POST localhost:8765/api/engine/sglang >/dev/null; wait_engine sglang
echo "engine: $(curl -s localhost:8765/api/engine)"

echo "== restart daemon =="; pkill -x cl-ps 2>/dev/null || true; sleep 2; start_daemon; sleep 5
echo "provision log (expect NO 'start failed'):"
grep -i provision /tmp/cl-ps.log | grep -iE 'vllm|sglang' || true
echo "engine after restart (expect active=sglang): $(curl -s localhost:8765/api/engine)"
echo "running engines (expect ONLY sglang): $(engines_running)"

echo "== switch back to vLLM (clean default) =="
curl -s -X POST localhost:8765/api/engine/vllm >/dev/null; wait_engine vllm
echo "engine: $(curl -s localhost:8765/api/engine)"
echo "running engines (expect ONLY vllm): $(engines_running)"
echo "PERSIST CHECK DONE"
