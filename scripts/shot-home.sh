#!/usr/bin/env bash
# Build the daemon (embeds the redesigned index.html), run it throwaway, and let the
# caller screenshot http://localhost:8799/. Prints when ready.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state" CLOUDLESS_ADDR="127.0.0.1:8799" CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8789"
BIN=/tmp/cl-shot
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-shot 2>/dev/null || true; sleep 0.4
sg docker -c "$BIN" >/tmp/cl-shot.log 2>&1 &
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done
echo "READY on 8799 (pid $(pgrep -x cl-shot))"
