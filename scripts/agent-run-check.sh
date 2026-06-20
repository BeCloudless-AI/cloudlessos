#!/usr/bin/env bash
# Smoke-test that the locally-built agent images start and their binaries resolve.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/agent-run-check.sh
set -uo pipefail
run() { sg docker -c "$*"; }

run "docker rm -f test-openclaw test-hermes" >/dev/null 2>&1 || true
run "docker network create cloudless" >/dev/null 2>&1 || true

echo "===== OpenClaw ====="
run "docker run -d --name test-openclaw --network cloudless cloudless/openclaw:local" >/dev/null 2>&1 || true
sleep 8
echo "state: $(run "docker inspect -f '{{.State.Status}}' test-openclaw" 2>&1)"
run "docker logs --tail 15 test-openclaw" 2>&1 | tail -15

echo "===== Hermes ====="
run "docker run -d --name test-hermes cloudless/hermes:local" >/dev/null 2>&1 || true
sleep 8
echo "state: $(run "docker inspect -f '{{.State.Status}}' test-hermes" 2>&1)"
run "docker logs --tail 15 test-hermes" 2>&1 | tail -15

run "docker rm -f test-openclaw test-hermes" >/dev/null 2>&1 || true
echo DONE
