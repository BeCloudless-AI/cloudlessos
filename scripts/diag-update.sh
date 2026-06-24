#!/usr/bin/env bash
# Validate the update-check mechanism (local vs remote image digest) against the
# images actually installed on this machine. Read-only: no pulls, no container changes.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/diag-update.sh
set -uo pipefail
run() { sg docker -c "$*"; }

check() {
  local image="$1"
  echo "=== $image ==="
  local local_d remote_d
  local_d=$(run "docker image inspect $image -f '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}'" 2>/dev/null | sed 's/.*@//')
  echo "  local : ${local_d:-<not pulled>}"
  remote_d=$(run "docker buildx imagetools inspect $image" 2>/dev/null | grep -m1 -i 'Digest:' | awk '{print $2}')
  echo "  remote: ${remote_d:-<unreachable>}"
  if [ -n "$local_d" ] && [ -n "$remote_d" ]; then
    if [ "$local_d" = "$remote_d" ]; then echo "  -> up to date"; else echo "  -> UPDATE AVAILABLE"; fi
  else
    echo "  -> can't compare"
  fi
}

check "ghcr.io/open-webui/open-webui:main"
check "mmartial/comfyui-nvidia-docker:latest"
echo "DIAG UPDATE DONE"
