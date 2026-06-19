#!/usr/bin/env bash
# Build + vet the Cloudless orchestrator. Run from WSL Ubuntu:
#     bash /mnt/d/Cloudless/scripts/build-orchestrator.sh
set -euo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
# Repo lives on /mnt/d (Windows FS) with mismatched ownership; skip git VCS stamping.
export GOFLAGS=-buildvcs=false
cd /mnt/d/Cloudless/orchestrator
echo "==> go version: $(go version)"
echo "==> go vet ./..."
go vet ./...
echo "==> go build ./..."
go build -o /tmp/cloudlessd ./cmd/cloudlessd
echo "==> built /tmp/cloudlessd ($(du -h /tmp/cloudlessd | cut -f1))"
echo "BUILD+VET OK"
