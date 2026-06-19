#!/usr/bin/env bash
# Build the orchestrator, run it, exercise the async install flow + lifecycle, clean up.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/smoke-test.sh
#
# Uses `sg docker` so it works before the docker-group login session is refreshed.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cloudlessd
echo "==> Building"
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )

run() { sg docker -c "$*"; }  # run a command with the docker group active

echo "==> Starting daemon"
sg docker -c "$BIN" &
trap 'pkill -x cloudlessd 2>/dev/null' EXIT  # sg orphans the child; kill by name

for i in $(seq 1 20); do
  curl -sf localhost:8765/api/health >/dev/null 2>&1 && break
  sleep 0.5
done

echo "--- /api/health ---";  curl -s localhost:8765/api/health;  echo
echo "--- /api/gpu ---";     curl -s localhost:8765/api/gpu;     echo
echo "--- /api/apps (before) ---"; curl -s localhost:8765/api/apps; echo

echo "==> POST /api/apps/ollama/start (async)"
JOB=$(curl -s -X POST localhost:8765/api/apps/ollama/start | grep -o '"jobId":"[^"]*"' | cut -d'"' -f4)
echo "    jobId=$JOB"

echo "==> Streaming job progress via SSE (/api/jobs/$JOB/events)"
timeout 120 curl -Ns "localhost:8765/api/jobs/$JOB/events"
echo

echo "--- /api/apps (after start) ---"; curl -s localhost:8765/api/apps; echo
echo "--- GPU attached? ---"
run "docker inspect cloudless-ollama --format '{{json .HostConfig.DeviceRequests}}'"

echo "==> Cleanup (stop + remove)"
curl -s -X POST localhost:8765/api/apps/ollama/stop;   echo
curl -s -X POST localhost:8765/api/apps/ollama/remove; echo
run "docker ps -a --format '{{.Names}}' | grep cloudless- || echo '(none — clean)'"

echo "SMOKE TEST DONE"
