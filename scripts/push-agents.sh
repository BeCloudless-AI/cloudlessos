#!/usr/bin/env bash
# Tag + push the locally-built agent images to Docker Hub, then print their digests.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/push-agents.sh
set -uo pipefail
NS=samuelcardillo
run() { sg docker -c "$*"; }

run "docker tag cloudless/openclaw:local $NS/cloudless-openclaw:v1"
run "docker tag cloudless/hermes:local   $NS/cloudless-hermes:v1"

echo "=== pushing cloudless-openclaw:v1 (988 MB) ==="
run "docker push $NS/cloudless-openclaw:v1"
echo "=== pushing cloudless-hermes:v1 (3.65 GB) ==="
run "docker push $NS/cloudless-hermes:v1"

echo "=== pushed digests ==="
printf 'openclaw  %s\n' "$(run "docker buildx imagetools inspect $NS/cloudless-openclaw:v1" 2>/dev/null | grep -m1 -i 'Digest:' | awk '{print $2}')"
printf 'hermes    %s\n' "$(run "docker buildx imagetools inspect $NS/cloudless-hermes:v1"   2>/dev/null | grep -m1 -i 'Digest:' | awk '{print $2}')"
echo "PUSH AGENTS DONE"
