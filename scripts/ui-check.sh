#!/usr/bin/env bash
# Build, run the daemon, and verify the web UI + vendored assets are served.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/ui-check.sh
set -euo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cloudlessd-ui
echo "==> Building"
( cd /mnt/d/Cloudless/orchestrator && go vet ./... && go build -o "$BIN" ./cmd/cloudlessd )
echo "    binary size: $(du -h "$BIN" | cut -f1)"

sg docker -c "$BIN" &
trap 'pkill -x cloudlessd-ui 2>/dev/null' EXIT  # sg orphans the child; kill by name
for i in $(seq 1 20); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "--- GET / ---"
curl -s -D - -o /dev/null localhost:8765/ | grep -iE 'HTTP/|content-type'
echo "--- GET /vendor/three.min.js ---"
curl -s -D - -o /dev/null localhost:8765/vendor/three.min.js | grep -iE 'HTTP/|content-type|content-length'
echo "--- key UI elements present in index ---"
curl -s localhost:8765/ | grep -oE 'vendor/three\.min\.js|id="onboarding"|canvas id="bg"|cloudless\.onboarded' | sort -u
echo "UI CHECK DONE"
