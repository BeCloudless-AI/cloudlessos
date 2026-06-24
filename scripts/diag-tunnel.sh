#!/usr/bin/env bash
# One-off diagnostic: does a Cloudflare quick tunnel actually work in this (WSL) env?
# Creates a throwaway tunnel to the running open-webui (localhost:3000), tests the
# full public round-trip, prints cloudflared's own status, then removes it.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/diag-tunnel.sh
set -uo pipefail
run() { sg docker -c "$*"; }

run "docker rm -f cl-diag-tunnel" >/dev/null 2>&1 || true
run "docker run -d --name cl-diag-tunnel --network host cloudflare/cloudflared:latest tunnel --no-autoupdate --url http://localhost:3000" >/dev/null 2>&1

url=""
for i in $(seq 1 40); do
  url=$(run "docker logs cl-diag-tunnel" 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1)
  [ -n "$url" ] && break
  sleep 1
done
echo "generated url : ${url:-<none>}"

if [ -n "$url" ]; then
  sleep 4   # give the edge a moment to become reachable
  curl -s -o /dev/null -L -w "public trip   : HTTP %{http_code} in %{time_total}s\n" --max-time 25 "$url/" || echo "public trip   : curl failed"
fi

echo "--- cloudflared status lines ---"
run "docker logs cl-diag-tunnel" 2>&1 | grep -iE 'registered|Registered tunnel connection|error|refus|unreachable|failed|ERR ' | tail -8

run "docker rm -f cl-diag-tunnel" >/dev/null 2>&1 || true
echo "(diagnostic tunnel removed)"
