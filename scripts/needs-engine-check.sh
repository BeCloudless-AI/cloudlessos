#!/usr/bin/env bash
# Confirm the new binary marks engine-dependent apps with needsEngine=true.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state" CLOUDLESS_ADDR="127.0.0.1:8799" CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8789"
BIN=/tmp/cl-ne
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-ne 2>/dev/null || true; sleep 0.4
sg docker -c "$BIN" >/tmp/cl-ne.log 2>&1 &
trap 'pkill -x cl-ne 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8799/api/health >/dev/null 2>&1 && break; sleep 0.5; done
curl -s localhost:8799/api/catalog | python3 -c 'import json,sys
d=json.load(sys.stdin)
for a in d:
  if a["id"] in ("open-webui","comfyui","openclaw","hermes","ai-toolkit","unsloth"):
    print("%-12s needsEngine=%s" % (a["id"], a.get("needsEngine", False)))'
echo DONE
