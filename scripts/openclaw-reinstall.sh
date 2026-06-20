#!/usr/bin/env bash
# Remove the stale OpenClaw image/container and reinstall from the latest code,
# confirming it comes up host-networked with no auth.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/openclaw-reinstall.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-ocr
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
sg docker -c "docker rm -f cloudless-openclaw" >/dev/null 2>&1 || true
sg docker -c "docker rmi -f cloudless/openclaw:local" >/dev/null 2>&1 || true
pkill -x cl-ocr 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-ocr.log 2>&1 &
trap 'pkill -x cl-ocr 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "==> installing OpenClaw (fresh build, no-auth, host networking)…"
curl -s -X POST localhost:8765/api/apps/openclaw/start >/dev/null
for i in $(seq 1 150); do sg docker -c "docker ps --format '{{.Names}}'" 2>/dev/null | grep -q '^cloudless-openclaw$' && break; sleep 2; done
sleep 4
echo "network mode    : $(sg docker -c "docker inspect -f '{{.HostConfig.NetworkMode}}' cloudless-openclaw" 2>&1)"
echo "token env count : $(sg docker -c "docker inspect cloudless-openclaw" 2>&1 | grep -c GATEWAY_TOKEN)"
echo "HTTP /          : $(curl -s -o /dev/null -w '%{http_code}' http://localhost:18789/)"
echo "--- gateway auth log ---"
sg docker -c "docker logs cloudless-openclaw" 2>&1 | grep -iE 'auth mode|refus|ready|agent model' | tail -4
echo "OPENCLAW REINSTALL DONE"
