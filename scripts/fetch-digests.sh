#!/usr/bin/env bash
# Print the current registry digest for each catalog image (no pull).
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/fetch-digests.sh
set -uo pipefail
run() { sg docker -c "$*"; }

digest() {
  local id="$1" image="$2"
  local d
  d=$(run "docker buildx imagetools inspect $image" 2>/dev/null | grep -m1 -i 'Digest:' | awk '{print $2}')
  printf '%-12s %-42s %s\n' "$id" "$image" "${d:-<unreachable>}"
}

digest vllm        "vllm/vllm-openai:latest"
digest sglang      "lmsysorg/sglang:latest"
digest open-webui  "ghcr.io/open-webui/open-webui:main"
digest comfyui     "mmartial/comfyui-nvidia-docker:latest"
digest ai-toolkit  "ostris/aitoolkit:latest"
digest unsloth     "unsloth/unsloth:latest"
digest openclaw    "samuelcardillo/cloudless-openclaw:v1"
digest hermes      "samuelcardillo/cloudless-hermes:v1"
echo "FETCH DIGESTS DONE"
