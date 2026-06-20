#!/usr/bin/env bash
# Verify smooth engine switching: the stable cloudless-ai endpoint always points
# at the active engine, so clients never change. Switches vLLM -> SGLang and
# confirms a completion still works through cloudless-ai (also tests SGLang on Blackwell).
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/engine-switch-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-sw
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-sw 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
bash /mnt/d/Cloudless/scripts/reset-apps.sh
sg docker -c "$BIN" >/tmp/cl-sw.log 2>&1 &
trap 'pkill -x cl-sw 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

DNS='import urllib.request,json;print(json.load(urllib.request.urlopen("http://cloudless-ai:8000/v1/models",timeout=5))["data"][0]["root"])'

echo "==> waiting for default engine (vLLM) to be ready…"
for i in $(seq 1 90); do curl -s localhost:8765/api/engine | grep -q '"ready":true' && break; sleep 2; done
echo "engine: $(curl -s localhost:8765/api/engine)"
for i in $(seq 1 60); do sg docker -c "docker ps --format '{{.Names}}'" | grep -q '^cloudless-open-webui$' && break; sleep 2; done
echo "cloudless-ai (from open-webui) serves: $(sg docker -c "docker exec cloudless-open-webui python3 -c '$DNS'" 2>&1)"

echo "==> SWITCHING to SGLang…"
JOB=$(curl -s -X POST localhost:8765/api/engine/sglang | grep -o '"jobId":"[^"]*"' | cut -d'"' -f4)
echo "  job=$JOB"
for i in $(seq 1 170); do
  r=$(curl -s localhost:8765/api/engine)
  if echo "$r" | grep -q '"active":"sglang"' && echo "$r" | grep -q '"ready":true'; then echo "  SGLang ready after ~$((i*2))s"; break; fi
  sleep 2
done
echo "engine: $(curl -s localhost:8765/api/engine)"
echo "cloudless-ai (from open-webui) serves: $(sg docker -c "docker exec cloudless-open-webui python3 -c '$DNS'" 2>&1)"

echo "==> completion through cloudless-ai (from open-webui):"
CHAT='import urllib.request,json;d=json.dumps({"model":"cloudless","messages":[{"role":"user","content":"Reply with exactly: switch ok"}],"max_tokens":16,"temperature":0}).encode();req=urllib.request.Request("http://cloudless-ai:8000/v1/chat/completions",data=d,headers={"Content-Type":"application/json"});print(json.load(urllib.request.urlopen(req,timeout=30))["choices"][0]["message"]["content"])'
sg docker -c "docker exec cloudless-open-webui python3 -c '$CHAT'" 2>&1 || echo "(completion failed)"

echo "--- sglang logs (tail) ---"
sg docker -c "docker logs --tail 15 cloudless-sglang" 2>&1 | tail -15
echo "ENGINE SWITCH CHECK DONE"
