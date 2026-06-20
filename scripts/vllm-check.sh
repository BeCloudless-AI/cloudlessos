#!/usr/bin/env bash
# Decisive check: does vLLM run on this GPU (Blackwell / RTX 50xx)? Starts the
# engine directly, waits for it to serve, and runs a chat completion.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/vllm-check.sh
set -uo pipefail
run() { sg docker -c "$*"; }
MODEL="${CLOUDLESS_DEFAULT_MODEL:-Qwen/Qwen2.5-1.5B-Instruct}"

run "docker network create cloudless" >/dev/null 2>&1 || true
run "docker rm -f cloudless-vllm" >/dev/null 2>&1 || true

echo "==> starting vLLM (entrypoint 'vllm serve'), model $MODEL"
run "docker run -d --name cloudless-vllm --gpus all --network cloudless -p 127.0.0.1:8000:8000 -v cloudless-hf:/root/.cache/huggingface vllm/vllm-openai:latest $MODEL --served-model-name cloudless --gpu-memory-utilization 0.5 --max-model-len 8192"

echo "==> waiting for /v1/models (downloads model on first run)…"
up=0
for i in $(seq 1 110); do
  if curl -sf localhost:8000/v1/models >/dev/null 2>&1; then up=1; break; fi
  if ! run "docker ps --format '{{.Names}}'" 2>/dev/null | grep -q '^cloudless-vllm$'; then echo "   container exited early!"; break; fi
  sleep 5
done
echo "   vLLM serving: $up (after ~$((i*5))s)"

echo "--- /v1/models ---"; curl -s localhost:8000/v1/models; echo
echo "--- chat completion ---"
curl -s localhost:8000/v1/chat/completions -H 'Content-Type: application/json' \
  -d '{"model":"cloudless","messages":[{"role":"user","content":"Reply with exactly: Cloudless AI online."}],"max_tokens":24,"temperature":0}'
echo
echo "--- vLLM logs (tail) ---"
run "docker logs --tail 30 cloudless-vllm" 2>&1 | tail -30
echo "VLLM CHECK DONE"
