#!/usr/bin/env bash
# Verify the diffusion (image model) manager: endpoint + the real download-into-ComfyUI-
# volume + "your models" scan, using a TINY file (not a multi-GB model).
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/diffusion-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_ADDR="127.0.0.1:8799"
export CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"
P=8799
BIN=/tmp/cl-df
run() { sg docker -c "$*"; }
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-df 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-df.log 2>&1 &
trap 'pkill -x cl-df 2>/dev/null || true; run "docker run --rm -v cloudless-comfyui:/c busybox rm -f /c/ComfyUI/models/checkpoints/_cloudless_test.safetensors" >/dev/null 2>&1 || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:$P/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "== JSON valid =="; python3 -c 'import json;json.load(open("/mnt/d/Cloudless/docs/cloudless-diffusion.json"));print("ok")'

echo "== /api/diffusion (before) =="
curl -s localhost:$P/api/diffusion | python3 -c '
import json,sys; d=json.load(sys.stdin)
print("GPU:",d["gpuVRAMGB"],"GB  yours:",len(d["yours"]),"  highlights:",len(d["highlights"]))
for m in d["highlights"]: print("   %6s ~%2sGB %-7s %-22s %s"%(m["fit"],m["minVramGB"],m["base"],m["name"],",".join(m.get("tags",[]))))
'

echo "== simulate a downloaded model (drop a dummy .safetensors into the volume) =="
sg docker -c "docker run --rm -v cloudless-comfyui:/c busybox sh -c 'mkdir -p /c/ComfyUI/models/checkpoints && echo dummy > /c/ComfyUI/models/checkpoints/_cloudless_test.safetensors'" && echo "wrote dummy model" || echo "FAILED"

echo "== /api/diffusion (after) — should appear under 'yours' =="
curl -s localhost:$P/api/diffusion | python3 -c '
import json,sys; d=json.load(sys.stdin)
print("yours:", [m["name"] for m in d["yours"]])
'
echo "DIFFUSION CHECK DONE"
