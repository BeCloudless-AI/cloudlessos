#!/usr/bin/env bash
# Verify per-app config: edit OpenClaw's config via the API and confirm the
# mounted file changes inside the container and it still starts.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/config-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false

BIN=/tmp/cl-cfg
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd )
pkill -x cl-cfg 2>/dev/null || true
pkill -x cloudlessd 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-cfg.log 2>&1 &
trap 'pkill -x cl-cfg 2>/dev/null || true; pkill -x docker 2>/dev/null || true' EXIT
for i in $(seq 1 60); do curl -sf localhost:8765/api/health >/dev/null 2>&1 && break; sleep 0.5; done

if ! sg docker -c "docker ps --format '{{.Names}}'" | grep -q '^cloudless-openclaw$'; then
  echo "installing openclaw…"
  curl -s -X POST localhost:8765/api/apps/openclaw/start >/dev/null
  for i in $(seq 1 90); do sg docker -c "docker ps --format '{{.Names}}'" | grep -q '^cloudless-openclaw$' && break; sleep 2; done
fi

echo "catalog config field: $(curl -s localhost:8765/api/catalog | grep -o '"config":\[[^]]*\]' | head -1)"
echo "==> edit + save config (prepend a json5 comment marker)…"
JOB=$(python3 - <<'PY'
import json, urllib.request
d = json.load(urllib.request.urlopen('http://localhost:8765/api/apps/openclaw/config'))
files = {f['file']: f['content'] for f in d['files']}
files['openclaw.json'] = '// cloudless-config-test-marker\n' + files['openclaw.json']
body = json.dumps({'files': files}).encode()
req = urllib.request.Request('http://localhost:8765/api/apps/openclaw/config', data=body,
                            headers={'Content-Type': 'application/json'}, method='POST')
print(json.load(urllib.request.urlopen(req)).get('jobId', ''))
PY
)
echo "  job=$JOB"
for i in $(seq 1 60); do curl -s "localhost:8765/api/jobs/$JOB" | grep -q '"done":true' && break; sleep 2; done
echo "==> container config first line (expect the marker):"
sg docker -c "docker exec cloudless-openclaw head -1 /root/.openclaw/openclaw.json" 2>&1
echo "==> still starts with no auth: $(sg docker -c "docker logs cloudless-openclaw" 2>&1 | grep -i 'auth mode' | tail -1)"
echo "CONFIG CHECK DONE"
