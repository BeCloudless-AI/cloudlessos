#!/usr/bin/env bash
# Dev utility: remove ALL Cloudless-managed containers and the shared network so
# the next daemon start provisions everything fresh (engines included).
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/reset-apps.sh
set -uo pipefail
ids=$(sg docker -c "docker ps -aq --filter name=cloudless-" | tr '\n' ' ')
if [ -n "${ids// /}" ]; then sg docker -c "docker rm -f $ids" >/dev/null; fi
sg docker -c "docker network rm cloudless" >/dev/null 2>&1 || true
echo "reset: removed all cloudless-* containers and the 'cloudless' network"
