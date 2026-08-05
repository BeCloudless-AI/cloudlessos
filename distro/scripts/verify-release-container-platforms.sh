#!/usr/bin/env bash
# Prove that this Docker daemon can execute every release architecture before
# the expensive exact-commit qualification begins.
set -euo pipefail

DOCKER="${CLOUDLESS_DOCKER:-docker}"
IMAGE="${CLOUDLESS_PLATFORM_PROBE_IMAGE:-ubuntu:24.04}"
command -v "$DOCKER" >/dev/null || {
    echo "Release container-platform check: missing Docker command: $DOCKER" >&2
    exit 1
}

probe() {
    local architecture="$1"
    local output
    if ! output="$($DOCKER run --rm --platform "linux/$architecture" "$IMAGE" uname -m 2>&1)"; then
        echo "Release container-platform check: Docker cannot execute linux/$architecture containers." >&2
        printf '%s\n' "$output" >&2
        echo "Register cross-architecture emulation, then rerun the release:" >&2
        echo "  docker run --privileged --rm tonistiigi/binfmt --install $architecture" >&2
        exit 1
    fi
    case "$architecture:$output" in
        amd64:x86_64|amd64:amd64|arm64:aarch64|arm64:arm64) ;;
        *)
            echo "Release container-platform check: linux/$architecture reported unexpected machine $output" >&2
            exit 1
            ;;
    esac
    echo "Docker can execute linux/$architecture release containers ($output)."
}

probe amd64
probe arm64
