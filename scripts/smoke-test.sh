#!/usr/bin/env bash
# Build the orchestrator, run it, exercise the full API + app lifecycle, clean up.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/smoke-test.sh
#
# Uses `sg docker` so it works before the docker-group login session is refreshed.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cloudlessd
echo "==> Building"
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )

run() {  # run a command with the docker group active
  sg docker -c "$*"
}

echo "==> Starting daemon"
sg docker -c "$BIN" &
DPID=$!
trap 'kill $DPID 2>/dev/null' EXIT

# wait for readiness
for i in $(seq 1 20); do
  curl -sf localhost:8765/api/health >/dev/null 2>&1 && break
  sleep 0.5
done

echo "--- /api/health ---";  curl -s localhost:8765/api/health;  echo
echo "--- /api/gpu ---";     curl -s localhost:8765/api/gpu;     echo
echo "--- /api/catalog ---"; curl -s localhost:8765/api/catalog; echo
echo "--- /api/apps (before) ---"; curl -s localhost:8765/api/apps; echo

echo "==> POST /api/apps/ollama/start  (pulls image on first run, please wait)"
curl -s -X POST localhost:8765/api/apps/ollama/start; echo

echo "--- /api/apps (after start) ---"; curl -s localhost:8765/api/apps; echo
echo "--- docker ps ---"
run "docker ps --format '{{.Names}} | {{.Image}} | {{.Status}}'"

echo "==> Confirm GPU is attached to the container"
run "docker inspect cloudless-ollama --format '{{json .HostConfig.DeviceRequests}}'"

echo "==> POST /api/apps/ollama/stop + remove (cleanup)"
curl -s -X POST localhost:8765/api/apps/ollama/stop;   echo
curl -s -X POST localhost:8765/api/apps/ollama/remove; echo
echo "--- docker ps -a (cloudless-*) ---"
run "docker ps -a --format '{{.Names}}' | grep cloudless- || echo '(none — clean)'"

echo "SMOKE TEST DONE"
