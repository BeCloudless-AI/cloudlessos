#!/usr/bin/env bash
# Build + test the power package and verify the whole tree still compiles.
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
cd /mnt/d/Cloudless/orchestrator || exit 1
echo "== test power =="
go test ./internal/power/ || exit 1
echo "== vet =="
go vet ./internal/power/ ./internal/api/ ./cmd/cloudlessd/ || exit 1
echo "== build all =="
go build ./... || exit 1
echo BUILD_OK
