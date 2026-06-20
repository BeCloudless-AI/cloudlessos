#!/usr/bin/env bash
# Reproduce the OpenClaw failure: send a tool-calling request to the active engine.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/tool-check.sh
set -uo pipefail
read -r -d '' BODY <<'JSON' || true
{"model":"cloudless","messages":[{"role":"user","content":"Weather in Paris?"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"Get the weather for a city","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}],"tool_choice":"auto","max_tokens":64}
JSON

echo "--- POST /v1/chat/completions WITH tools ---"
curl -s -X POST http://localhost:8000/v1/chat/completions -H 'Content-Type: application/json' -d "$BODY"
echo
echo "--- engine logs (tail) ---"
sg docker -c "docker logs --tail 12 cloudless-vllm" 2>&1 | tail -12 || true
