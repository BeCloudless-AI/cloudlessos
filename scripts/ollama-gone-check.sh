#!/usr/bin/env bash
# Confirm Ollama is removed from the catalog and the bundled web apps persist data.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/ollama-gone-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_ADDR="127.0.0.1:8799"
export CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"
P=8799
BIN=/tmp/cl-oc
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-oc 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-oc.log 2>&1 &
trap 'pkill -x cl-oc 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:$P/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "ollama in catalog : $(curl -s localhost:$P/api/catalog | grep -oc '\"id\":\"ollama\"')  (expect 0)"
echo "app ids           : $(curl -s localhost:$P/api/catalog | grep -oE '\"id\":\"[a-z0-9-]+\"' | tr '\n' ' ')"
echo "OLLAMA CHECK DONE"
