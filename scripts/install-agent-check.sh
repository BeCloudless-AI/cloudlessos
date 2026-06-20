#!/usr/bin/env bash
# Verify the daemon install path for a build-app: POST start builds the image
# from the embedded context and runs it, pre-wired to Cloudless AI.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/install-agent-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-ia
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-ia 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-ia.log 2>&1 &
trap 'pkill -x cl-ia 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "==> POST /api/apps/openclaw/start (builds from embedded context, then runs)"
curl -s -X POST localhost:8765/api/apps/openclaw/start; echo
for i in $(seq 1 90); do
  if sg docker -c "docker ps --format '{{.Names}}'" 2>/dev/null | grep -q '^cloudless-openclaw$'; then break; fi
  sleep 2
done
echo "--- cloudless-openclaw status ---"
sg docker -c "docker ps --format '{{.Names}}  {{.Status}}'" | grep cloudless-openclaw || echo "(not running)"
echo "--- model wiring (expect custom/cloudless) ---"
sg docker -c "docker logs --tail 40 cloudless-openclaw" 2>&1 | grep -i 'agent model' || true
echo "INSTALL AGENT CHECK DONE"
