#!/usr/bin/env bash
set -euo pipefail

base_url="${1:-http://127.0.0.1:8890}"
api_key="${2:-}"
authorization=()
if [[ -n "$api_key" ]]; then
  authorization=(-H "Authorization: Bearer $api_key")
fi

request() {
  local label="$1"
  local body="$2"
  local response
  response="$(curl --fail-with-body --silent --show-error --max-time 300 \
    "${authorization[@]}" -H 'Content-Type: application/json' \
    -d "$body" "$base_url/v1/chat/completions")"
  RESPONSE="$response" LABEL="$label" python3 - <<'PY'
import json
import os

label = os.environ["LABEL"]
payload = json.loads(os.environ["RESPONSE"])
if "error" in payload:
    raise SystemExit(f"{label} failed: {payload['error']}")
choices = payload.get("choices")
if not isinstance(choices, list) or not choices:
    raise SystemExit(f"{label} returned no choices")
message = choices[0].get("message") or {}
if label == "structured-json":
    content = message.get("content")
    try:
        structured = json.loads(content)
    except (TypeError, json.JSONDecodeError) as exc:
        raise SystemExit(f"{label} returned invalid JSON content: {exc}") from exc
    if not isinstance(structured.get("city"), str) or not isinstance(structured.get("country"), str):
        raise SystemExit(f"{label} did not satisfy the requested schema")
if label == "tool-calling":
    tool_calls = message.get("tool_calls")
    if not isinstance(tool_calls, list) or not tool_calls:
        raise SystemExit(f"{label} returned no tool call")
    function = tool_calls[0].get("function") or {}
    if function.get("name") != "get_weather":
        raise SystemExit(f"{label} returned the wrong function")
    try:
        arguments = json.loads(function.get("arguments", ""))
    except json.JSONDecodeError as exc:
        raise SystemExit(f"{label} returned invalid tool arguments: {exc}") from exc
    if not isinstance(arguments.get("city"), str):
        raise SystemExit(f"{label} omitted the required city argument")
print(f"{label}: passed")
PY
}

request normal-chat '{
  "model":"cloudless",
  "messages":[{"role":"user","content":"Reply with exactly: READY"}],
  "temperature":0,
  "max_tokens":16
}'

request structured-json '{
  "model":"cloudless",
  "messages":[{"role":"user","content":"Return Paris and France."}],
  "temperature":0,
  "max_tokens":64,
  "response_format":{"type":"json_schema","json_schema":{"name":"place","strict":true,"schema":{"type":"object","properties":{"city":{"type":"string"},"country":{"type":"string"}},"required":["city","country"],"additionalProperties":false}}}
}'

request tool-calling '{
  "model":"cloudless",
  "messages":[{"role":"user","content":"What is the weather in Paris?"}],
  "temperature":0,
  "max_tokens":100,
  "tool_choice":"required",
  "tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}}}]
}'

echo 'All DeepSeek SparkInfer compatibility smoke tests passed.'
