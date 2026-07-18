#!/usr/bin/env bash
set -e
export PATH="/usr/local/go/bin:$HOME/go/bin:/usr/bin:/bin"
export GOFLAGS=-buildvcs=false
cd /mnt/d/Cloudless/orchestrator
go build ./... && echo BUILD_OK
