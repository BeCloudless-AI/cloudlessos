#!/usr/bin/env bash
# Build, run the daemon, and verify the redesigned UI + new endpoints/assets.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/ui-check.sh
set -euo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-ui   # short name so `pkill -x` (15-char comm limit) works
echo "==> vet + build"
( cd /mnt/d/Cloudless/orchestrator && go vet ./... && go build -o "$BIN" ./cmd/cloudlessd )
echo "    binary: $(du -h "$BIN" | cut -f1)"

pkill -x cl-ui 2>/dev/null || true
sg docker -c "$BIN" &
trap 'pkill -x cl-ui 2>/dev/null' EXIT
for i in $(seq 1 20); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.3; done

code() { curl -s -o /dev/null -w "%{http_code}" "localhost:8765$1"; }
echo "--- asset status codes ---"
echo "  /                                         -> $(code /)"
echo "  /vendor/three.min.js                      -> $(code /vendor/three.min.js)"
echo "  /vendor/fonts/red-hat-mono-latin-400-normal.woff2 -> $(code /vendor/fonts/red-hat-mono-latin-400-normal.woff2)"
echo "--- api payloads ---"
echo "  /api/gpu       -> $(curl -s localhost:8765/api/gpu)"
echo "  /api/folders   -> $(curl -s localhost:8765/api/folders)"
echo "  /api/onboarding-> $(curl -s localhost:8765/api/onboarding)"
echo "UI CHECK DONE"
