#!/usr/bin/env bash
# Verify the Model Manager endpoint: GPU VRAM detection + per-model fit verdict.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/models-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_ADDR="127.0.0.1:8799"
export CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"
P=8799
BIN=/tmp/cl-mm
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-mm 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-mm.log 2>&1 &
trap 'pkill -x cl-mm 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:$P/api/health >/dev/null 2>&1 && break; sleep 0.5; done

curl -s localhost:$P/api/models | python3 -c '
import json,sys
d=json.load(sys.stdin)
print("GPU VRAM:", d["gpuVRAMGB"], "GB   current:", d["current"])
def row(m):
    caps = ("V" if m.get("vision") else "-") + ("T" if m.get("toolCalling") else "-")
    star = " [ACTIVE]" if m["active"] else (" (on disk)" if m.get("downloaded") else "")
    print("   %6s  %s  ~%2sGB  %-22s %s%s" % (m["fit"], caps, m["minVramGB"], m["name"], ",".join(m.get("tags",[])), star))
print("YOUR MODELS:")
for m in d["yours"]: row(m)
print("CLOUDLESS HIGHLIGHTS:")
for m in d["highlights"]: row(m)
'
echo "MODELS CHECK DONE"
