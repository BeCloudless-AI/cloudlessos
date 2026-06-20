#!/usr/bin/env bash
# Verify the pre-install provisioner: shared network + Ollama + Open WebUI wired
# and branded, with Open WebUI able to reach Ollama by container DNS.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/provision-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_DEFAULT_MODEL=""   # skip the heavy default-model pull for this check

BIN=/tmp/cl-prov
echo "==> build"
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )

pkill -x cl-prov 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-prov.log 2>&1 &
# kill the daemon AND any orphaned `docker pull` (e.g. ComfyUI) on exit
trap 'pkill -x cl-prov 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT

echo "==> waiting for Open WebUI (first run pulls ~3.7GB)…"
ok=0
for i in $(seq 1 360); do
  if sg docker -c "docker ps --format '{{.Names}}'" 2>/dev/null | grep -q '^cloudless-open-webui$'; then ok=1; break; fi
  sleep 1
done
echo "   waited ${i}s; open-webui up: $ok"

echo "--- provision log ---"; grep -i 'provision' /tmp/cl-prov.log || true
echo "--- network 'cloudless' members ---"
sg docker -c "docker network inspect cloudless --format '{{range .Containers}}{{.Name}}  {{end}}'"
echo "--- open-webui branding + wiring env ---"
sg docker -c "docker inspect cloudless-open-webui --format '{{json .Config.Env}}'" | tr ',' '\n' | grep -iE 'WEBUI_NAME|WEBUI_AUTH|OLLAMA_BASE_URL' || true
echo "--- open-webui -> ollama (by container DNS) ---"
sg docker -c "docker exec cloudless-open-webui python3 -c \"import urllib.request;print(urllib.request.urlopen('http://cloudless-ollama:11434/',timeout=5).read().decode())\"" 2>&1 || echo "(reachability check failed)"
echo "PROVISION CHECK DONE"
