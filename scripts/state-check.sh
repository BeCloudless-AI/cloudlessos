#!/usr/bin/env bash
# Verify server-side first-run state: fresh -> complete -> persists across restart.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/state-check.sh
set -euo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-state
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"   # isolated, guarantees a fresh first run
echo "==> state dir: $CLOUDLESS_STATE_DIR"

( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-state 2>/dev/null || true
trap 'pkill -x cl-state 2>/dev/null' EXIT

start() {
  "$BIN" >/tmp/cl-state.log 2>&1 &
  for i in $(seq 1 20); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && return 0; sleep 0.3; done
  echo "daemon did not become ready"; cat /tmp/cl-state.log; return 1
}

echo "== launch 1 (fresh install) =="; start
grep -i 'state:' /tmp/cl-state.log
echo "GET  /api/onboarding         -> $(curl -s localhost:8765/api/onboarding)"
echo "POST /api/onboarding/complete -> $(curl -s -X POST localhost:8765/api/onboarding/complete)"
echo "GET  /api/onboarding         -> $(curl -s localhost:8765/api/onboarding)"
echo "state.json on disk:"; cat "$CLOUDLESS_STATE_DIR/state.json"; echo
pkill -x cl-state; sleep 1

echo "== launch 2 (restart — should be remembered) =="; start
grep -i 'state:' /tmp/cl-state.log
echo "GET  /api/onboarding         -> $(curl -s localhost:8765/api/onboarding)"
echo "STATE CHECK DONE"
