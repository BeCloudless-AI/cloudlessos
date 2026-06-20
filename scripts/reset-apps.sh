#!/usr/bin/env bash
# Dev utility: remove Cloudless-managed containers and the shared network so the
# next daemon start provisions everything fresh.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/reset-apps.sh
set -uo pipefail
sg docker -c "docker rm -f cloudless-ollama cloudless-open-webui cloudless-comfyui" 2>/dev/null || true
sg docker -c "docker network rm cloudless" 2>/dev/null || true
echo "reset: cloudless-* containers and 'cloudless' network removed"
