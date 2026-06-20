#!/usr/bin/env bash
# Verify the form-based app settings: fields read defaults, and saving writes the
# right config (Hermes -> .env keys, OpenClaw -> openclaw.json path).
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/settings-form-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"   # isolated, so we can inspect writes

BIN=/tmp/cl-sf
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-sf 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-sf.log 2>&1 &
trap 'pkill -x cl-sf 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "hermes settings  : $(curl -s localhost:8765/api/apps/hermes/settings)"
echo "openclaw settings: $(curl -s localhost:8765/api/apps/openclaw/settings)"

echo "==> save hermes form (allow all + telegram)…"
curl -s -X POST localhost:8765/api/apps/hermes/settings -H 'Content-Type: application/json' \
  -d '{"values":{"allowAll":"true","tgToken":"abc123","tgUsers":"42,99"}}'; echo
echo "--- hermes.env ---"; cat "$CLOUDLESS_STATE_DIR/apps/hermes/hermes.env" 2>&1

echo "==> save openclaw form (endpoint + name)…"
curl -s -X POST localhost:8765/api/apps/openclaw/settings -H 'Content-Type: application/json' \
  -d '{"values":{"baseUrl":"http://example:9000/v1","alias":"My AI"}}'; echo
echo "--- openclaw.json ---"; cat "$CLOUDLESS_STATE_DIR/apps/openclaw/openclaw.json" 2>&1

echo "UI form bits: $(curl -s localhost:8765/ | grep -oE 'fld-form|fieldControl|adv-toggle|class=\"switch\"' | sort -u | tr '\n' ' ')"
echo "SETTINGS FORM CHECK DONE"
