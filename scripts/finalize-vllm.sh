#!/usr/bin/env bash
# Leave the system on vLLM (clean default) and confirm vLLM tool calling.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/finalize-vllm.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-fin
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-fin 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-fin.log 2>&1 &
trap 'pkill -x cl-fin 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "==> switching to vLLM (clean default)…"
curl -s -X POST localhost:8765/api/engine/vllm >/dev/null
for i in $(seq 1 150); do
  r=$(curl -s localhost:8765/api/engine)
  echo "$r" | grep -q '"active":"vllm"' && echo "$r" | grep -q '"ready":true' && break
  sleep 2
done
echo "engine: $(curl -s localhost:8765/api/engine)"
BODY='{"model":"cloudless","messages":[{"role":"user","content":"Weather in Paris?"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"Get the weather for a city","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}],"tool_choice":"auto","max_tokens":64}'
echo "vLLM tool call: $(curl -s -X POST http://localhost:8000/v1/chat/completions -H 'Content-Type: application/json' -d "$BODY" | grep -o '"name":"get_weather"[^}]*}' | head -1)"
echo "FINALIZE DONE"
