#!/usr/bin/env bash
# Build and confirm the restyled UI is embedded and served.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/render-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
BIN=/tmp/cl-rnd
( cd /mnt/d/Cloudless/orchestrator && go vet ./... && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-rnd 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-rnd.log 2>&1 &
trap 'pkill -x cl-rnd 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "GET /     : $(curl -s -o /dev/null -w '%{http_code}' localhost:8765/)"
echo "font woff2: $(curl -s -o /dev/null -w '%{http_code}' localhost:8765/vendor/fonts/red-hat-mono-latin-700-normal.woff2)"
echo "markers   : $(curl -s localhost:8765/ | grep -oE 'big-clock|accent-dot|--blue:|#ff5a00|backdrop-filter' | sort | uniq -c | tr '\n' ' ')"
echo "RENDER CHECK DONE"
