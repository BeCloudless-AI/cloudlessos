#!/usr/bin/env bash
# Verify machine-info / locale / profile / France->Mistral on the throwaway daemon.
# Run from WSL:  bash /mnt/d/Cloudless/scripts/locale-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_NO_PROVISION=1
P=8799
BIN=/tmp/cl-loc
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1

run_with() { # $1=TZ $2=LANG  -> starts daemon, waits, returns once healthy
  pkill -x cl-loc 2>/dev/null || true; sleep 0.4
  CLOUDLESS_STATE_DIR="$(mktemp -d)/state" TZ="$1" LANG="$2" LC_ALL="$2" \
    CLOUDLESS_ADDR="127.0.0.1:$P" sg docker -c "$BIN" >/tmp/cl-loc.log 2>&1 &
  for i in $(seq 1 40); do curl -sf localhost:$P/api/health >/dev/null 2>&1 && break; sleep 0.5; done
}

echo "############ Machine in FRANCE (TZ=Europe/Paris, LANG=fr_FR) ############"
run_with "Europe/Paris" "fr_FR.UTF-8"
echo "== /api/system =="
curl -s localhost:$P/api/system | python3 -c 'import json,sys;d=json.load(sys.stdin);s=d["system"];l=d["locale"];print("host=%s os=%s kernel=%s"%(s["hostname"],s["os"],s["kernel"]));print("cpu=%s cores=%s ramMB=%s arch=%s uptimeSec=%s"%(s["cpu"],s["cores"],s["memTotalMB"],s["arch"],s["uptimeSec"]));print("tz=%s offset=%s locale=%s country=%s(%s) src=%s"%(l["timezone"],l["utcOffset"],l["locale"],l["country"],l["countryName"],l["source"]))'
echo "== /api/profile =="
curl -s localhost:$P/api/profile | python3 -c 'import json,sys;d=json.load(sys.stdin);print("name=%r region(override)=%r effective=%s(%s) inFrance=%s src=%s"%(d["name"],d["region"],d["country"],d["countryName"],d["inFrance"],d["countrySource"]))'
echo "== /api/models region + recommended =="
curl -s localhost:$P/api/models | python3 -c 'import json,sys;d=json.load(sys.stdin);print("region:",d["region"]);recs=[m for m in d["highlights"] if m.get("recommended")];print("recommended highlights:",[(m["name"],m["recommendBy"],"gated" if m.get("gated") else "open") for m in recs]);print("first 3 highlights:",[m["name"] for m in d["highlights"][:3]])'

echo
echo "############ Machine in the US (TZ=America/New_York, LANG=en_US) ############"
run_with "America/New_York" "en_US.UTF-8"
curl -s localhost:$P/api/profile | python3 -c 'import json,sys;d=json.load(sys.stdin);print("effective=%s(%s) inFrance=%s"%(d["country"],d["countryName"],d["inFrance"]))'
curl -s localhost:$P/api/models | python3 -c 'import json,sys;d=json.load(sys.stdin);print("region:",d["region"]);print("any recommended:",any(m.get("recommended") for m in d["highlights"]));print("mistral present:",any("Mistral" in m["name"] for m in d["highlights"]))'

pkill -x cl-loc 2>/dev/null || true
echo "DONE"
