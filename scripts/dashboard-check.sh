#!/usr/bin/env bash
# Verify the dashboard rework: three.js asset, hourly themes, seconds clock,
# engine choice removed from home, Open WebUI hidden from the launcher.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/dashboard-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
BIN=/tmp/cl-vfy
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-vfy 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-vfy.log 2>&1 &
trap 'pkill -x cl-vfy 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "three.min.js  : $(curl -s -o /dev/null -w '%{http_code}' localhost:8765/vendor/three.min.js)"
echo "engine-row    : $(curl -s localhost:8765/ | grep -c 'engine-row' || true) (expect 0)"
echo "canvas id=bg  : $(curl -s localhost:8765/ | grep -c 'id=\"bg\"' || true) (expect 1)"
echo "data-theme    : $(curl -s localhost:8765/ | grep -oc 'data-theme=' || true) (themes + applyTheme)"
echo "getSeconds    : $(curl -s localhost:8765/ | grep -oc 'getSeconds' || true)"
echo "--line var    : $(curl -s localhost:8765/ | grep -oc '\-\-line:' || true)"
echo "hidden:true ct: $(curl -s localhost:8765/api/catalog | grep -oc '\"hidden\":true' || true) (expect 1 = open-webui)"
echo "DASHBOARD CHECK DONE"
