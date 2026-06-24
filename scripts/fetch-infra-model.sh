#!/usr/bin/env bash
# Fetch digests for infra images + the default model's HF revision.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/fetch-infra-model.sh
set -uo pipefail
run() { sg docker -c "$*"; }
digest() { run "docker buildx imagetools inspect $1" 2>/dev/null | grep -m1 -i 'Digest:' | awk '{print $2}'; }

printf '%-12s %-30s %s\n' cloudflared "cloudflare/cloudflared:latest" "$(digest cloudflare/cloudflared:latest)"
printf '%-12s %-30s %s\n' socat       "alpine/socat:latest"          "$(digest alpine/socat:latest)"

sha=$(curl -s https://huggingface.co/api/models/Qwen/Qwen2.5-1.5B-Instruct | python3 -c "import json,sys;print(json.load(sys.stdin).get('sha',''))" 2>/dev/null)
printf '%-12s %-30s %s\n' qwen-model  "Qwen/Qwen2.5-1.5B-Instruct" "${sha:-<unreachable>}"
echo "FETCH INFRA+MODEL DONE"
