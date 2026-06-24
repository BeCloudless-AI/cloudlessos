#!/usr/bin/env bash
# Print the authoritative repo digest of the pushed agent images (what the daemon
# compares against). Run from WSL Ubuntu.
set -uo pipefail
run() { sg docker -c "$*"; }
for img in samuelcardillo/cloudless-openclaw:v1 samuelcardillo/cloudless-hermes:v1; do
  echo "=== $img ==="
  rd=$(run "docker image inspect $img -f '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}'" 2>/dev/null | sed 's/.*@//')
  bx=$(run "docker buildx imagetools inspect $img" 2>/dev/null | grep -m1 -i 'Digest:' | awk '{print $2}')
  echo "  RepoDigest : ${rd:-<none>}"
  echo "  buildx     : ${bx:-<none>}"
done
echo "AGENT DIGESTS DONE"
