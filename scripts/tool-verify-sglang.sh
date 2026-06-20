#!/usr/bin/env bash
# Clean verify: tool calling works on BOTH engines and the switch is race-free.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/tool-verify-sglang.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-tvs
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-tvs 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
bash /mnt/d/Cloudless/scripts/reset-apps.sh
sg docker -c "$BIN" >/tmp/cl-tvs.log 2>&1 &
trap 'pkill -x cl-tvs 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

BODY='{"model":"cloudless","messages":[{"role":"user","content":"Weather in Paris?"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"Get the weather for a city","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}],"tool_choice":"auto","max_tokens":64}'
toolcall() { curl -s -X POST http://localhost:8000/v1/chat/completions -H 'Content-Type: application/json' -d "$BODY" | grep -o '"name":"get_weather"[^}]*}' | head -1; }
wait_ready() { for i in $(seq 1 150); do r=$(curl -s localhost:8765/api/engine); echo "$r" | grep -q "\"active\":\"$1\"" && echo "$r" | grep -q '"ready":true' && return; sleep 2; done; }

echo "==> waiting for default engine (vLLM) ready (provisioning settles)…"
wait_ready vllm
echo "engine: $(curl -s localhost:8765/api/engine)"
echo "vLLM tool call  : $(toolcall)"

echo "==> switching to SGLang…"
curl -s -X POST localhost:8765/api/engine/sglang >/dev/null
wait_ready sglang
echo "engine: $(curl -s localhost:8765/api/engine)"
echo "SGLang tool call: $(toolcall)"
echo "TOOL VERIFY SGLANG DONE"
