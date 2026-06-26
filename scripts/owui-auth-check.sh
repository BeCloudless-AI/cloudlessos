#!/usr/bin/env bash
# Verify Open WebUI auth settings + admin info on the throwaway daemon (no provision,
# so no OWUI container is touched — restartApp no-ops to "saved"). Run from WSL:
#   bash /mnt/d/Cloudless/scripts/owui-auth-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_NO_PROVISION=1
SD="$(mktemp -d)/state"
export CLOUDLESS_STATE_DIR="$SD"
export CLOUDLESS_ADDR="127.0.0.1:8799"
P=8799; BIN=/tmp/cl-owui
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-owui 2>/dev/null || true; sleep 0.4
sg docker -c "$BIN" >/tmp/cl-owui.log 2>&1 &
trap 'pkill -x cl-owui 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:$P/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "== settings (fields + admin) =="
curl -s localhost:$P/api/apps/open-webui/settings | python3 -c '
import json,sys; d=json.load(sys.stdin)
print("fields:", [(f["key"], f["type"], f["value"]) for f in d["fields"]])
a=d.get("admin"); print("admin.user:", a and a["user"]); print("admin.pass:", a and a["pass"])
print("admin.note[:60]:", (a and a["note"][:60]))
'
echo "== turn ON require-auth =="
curl -s -o /dev/null -X POST localhost:$P/api/apps/open-webui/settings -H 'Content-Type: application/json' -d '{"values":{"requireAuth":"true"}}'
echo "== webui.env on disk =="
cat "$SD/apps/open-webui/webui.env" 2>/dev/null || echo "(file missing)"
echo "== settings after (requireAuth should be true) =="
curl -s localhost:$P/api/apps/open-webui/settings | python3 -c 'import json,sys;d=json.load(sys.stdin);print([(f["key"],f["value"]) for f in d["fields"]])'
echo "DONE"
