#!/usr/bin/env bash
# Verify the Cloudless Proxy (API gateway): key management + auth + usage counting.
# No engine runs here (NO_PROVISION), so an authorized call reaches the proxy and
# returns 502 (engine absent) — which still proves auth passed + usage incremented.
# Run from WSL:  bash /mnt/d/Cloudless/scripts/gateway-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_NO_PROVISION=1
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"
export CLOUDLESS_ADDR="127.0.0.1:8799"
export CLOUDLESS_GATEWAY_ADDR="127.0.0.1:8788"
D=8799; G=8788; BIN=/tmp/cl-gw
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-gw 2>/dev/null || true; sleep 0.4
sg docker -c "$BIN" >/tmp/cl-gw.log 2>&1 &
trap 'pkill -x cl-gw 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:$D/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "== gateway info (before keys) =="
curl -s localhost:$D/api/gateway | python3 -c 'import json,sys;d=json.load(sys.stdin);print("port=%s servedName=%s model=%s keys=%d"%(d["port"],d["servedName"],d["model"],len(d["keys"])));print("localURL=",d["localURL"])'

echo "== create a key =="
KEY=$(curl -s -X POST localhost:$D/api/keys -H 'Content-Type: application/json' -d '{"name":"Test key"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["key"])')
echo "issued: ${KEY:0:24}…  (len=${#KEY})"

echo "== gateway proxy auth =="
echo -n "no key   -> "; curl -s -o /dev/null -w "%{http_code}\n" localhost:$G/v1/models
echo -n "bad key  -> "; curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer sk-cloudless-nope" localhost:$G/v1/models
echo -n "good key -> "; curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $KEY" localhost:$G/v1/models   # 502: auth OK, engine absent

echo "== hit twice more with the good key =="
curl -s -o /dev/null -H "Authorization: Bearer $KEY" localhost:$G/v1/chat/completions -d '{"model":"x","messages":[]}'
curl -s -o /dev/null -H "Authorization: Bearer $KEY" localhost:$G/v1/chat/completions -d '{"model":"x","messages":[]}'

echo "== flush usage (force a state save) + read back =="
# usage is flushed on a 20s timer or shutdown; give the daemon a moment then read
curl -s localhost:$D/api/gateway | python3 -c 'import json,sys;d=json.load(sys.stdin);k=d["keys"][0] if d["keys"] else {};print("key:",k.get("name"),"prefix:",k.get("prefix"),"requests(in-mem via API):",k.get("requests"),"lastUsed:",k.get("lastUsed"))'

echo "== revoke + verify rejected =="
ID=$(curl -s localhost:$D/api/gateway | python3 -c 'import json,sys;print(json.load(sys.stdin)["keys"][0]["id"])')
curl -s -o /dev/null -X DELETE localhost:$D/api/keys/$ID
echo -n "revoked key now -> "; curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $KEY" localhost:$G/v1/models
echo "DONE"
