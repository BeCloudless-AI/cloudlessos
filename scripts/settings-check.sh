#!/usr/bin/env bash
# Verify the Settings backend: /api/settings, onboarding reset, and the headline
# "reset OpenClaw" flow (rebuild + run). Run from WSL Ubuntu:
#   bash /mnt/d/Cloudless/scripts/settings-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-set
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-set 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-set.log 2>&1 &
trap 'pkill -x cl-set 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "GET /api/settings : $(curl -s localhost:8765/api/settings)"
echo "UI settings bits  : $(curl -s localhost:8765/ | grep -oE 'id=\"gear\"|set-body|renderSettings' | sort -u | tr '\n' ' ')"
echo "onboarding reset  : $(curl -s -X POST localhost:8765/api/onboarding/reset)"
echo "onboarding state  : $(curl -s localhost:8765/api/onboarding)"
curl -s -X POST localhost:8765/api/onboarding/complete >/dev/null  # restore

echo "==> reset OpenClaw (remove + rebuild + run)…"
JOB=$(curl -s -X POST localhost:8765/api/apps/openclaw/reset | grep -o '"jobId":"[^"]*"' | cut -d'"' -f4)
echo "  job=$JOB"
for i in $(seq 1 120); do curl -s "localhost:8765/api/jobs/$JOB" | grep -q '"done":true' && break; sleep 2; done
echo "  job done: $(curl -s localhost:8765/api/jobs/$JOB)"
echo "openclaw status   : $(sg docker -c "docker ps --format '{{.Names}} {{.Status}}'" | grep openclaw || echo none)"
echo "openclaw auth     : $(sg docker -c "docker logs cloudless-openclaw" 2>&1 | grep -iE 'auth mode' | tail -1)"
echo "SETTINGS CHECK DONE"
