#!/usr/bin/env bash
# Verify the pre-install provisioner: shared network + vLLM engine + Open WebUI
# wired to vLLM over the OpenAI API, with Open WebUI able to reach vLLM by DNS.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/provision-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-prov
echo "==> build"
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )

pkill -x cl-prov 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-prov.log 2>&1 &
trap 'pkill -x cl-prov 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT

echo "==> waiting for Open WebUI…"
for i in $(seq 1 120); do
  if sg docker -c "docker ps --format '{{.Names}}'" 2>/dev/null | grep -q '^cloudless-open-webui$'; then break; fi
  sleep 1
done
sleep 2

echo "--- provision log ---"; grep -i 'provision' /tmp/cl-prov.log || true
echo "--- network 'cloudless' members ---"
sg docker -c "docker network inspect cloudless --format '{{range .Containers}}{{.Name}}  {{end}}'"
echo "--- open-webui branding + OpenAI wiring env ---"
sg docker -c "docker inspect cloudless-open-webui --format '{{json .Config.Env}}'" | tr ',' '\n' | grep -iE 'WEBUI_NAME|WEBUI_AUTH|OPENAI_API_BASE_URL|ENABLE_OLLAMA' || true
echo "--- open-webui -> vLLM model list (by container DNS) ---"
sg docker -c "docker exec cloudless-open-webui python3 -c \"import urllib.request,json;print(json.load(urllib.request.urlopen('http://cloudless-vllm:8000/v1/models',timeout=5))['data'][0]['id'])\"" 2>&1 || echo "(reachability check failed)"
echo "PROVISION CHECK DONE"
