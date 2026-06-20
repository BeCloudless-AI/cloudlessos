#!/usr/bin/env bash
# Confirm OpenClaw runs plug-and-play: host networking + loopback bind + no auth,
# reachable at localhost:18789 with nothing to enter, wired to the active engine.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/openclaw-check.sh
set -uo pipefail
run() { sg docker -c "$*"; }

run "docker build -q -t cloudless/openclaw:local /mnt/d/Cloudless/orchestrator/internal/apps/openclaw" >/dev/null && echo BUILT
run "docker rm -f cloudless-openclaw t-oc" >/dev/null 2>&1 || true
run "docker run -d --name t-oc --network host cloudless/openclaw:local" >/dev/null
sleep 10
echo "state    : $(run "docker inspect -f '{{.State.Status}}' t-oc" 2>&1)"
echo "HTTP /   : $(curl -s -o /dev/null -w '%{http_code}' http://localhost:18789/)"
echo "--- gateway logs (auth / bind / model) ---"
run "docker logs t-oc" 2>&1 | grep -iE 'auth|refus|bind|ready|agent model|listening|http server' | tail -20
run "docker rm -f t-oc" >/dev/null 2>&1 || true
