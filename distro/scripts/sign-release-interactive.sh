#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-}"
CHANNEL="${2:-stable}"
SECRET_KEY="${CLOUDLESS_ARCHIVE_SECRET:-/mnt/d/CloudlessSecrets/cloudless-archive-secret.asc}"

if [ -z "$VERSION" ]; then
    echo "Usage: $0 VERSION [stable|beta]" >&2
    exit 2
fi
test -s "$SECRET_KEY" || { echo "Signing-key backup not found: $SECRET_KEY" >&2; exit 1; }
command -v docker >/dev/null || { echo "Docker is required." >&2; exit 1; }

secret_dir="$(dirname "$(realpath "$SECRET_KEY")")"
secret_name="$(basename "$SECRET_KEY")"

docker image inspect cloudless-release-builder >/dev/null 2>&1 || \
    docker build -t cloudless-release-builder \
        -f "$ROOT/distro/docker/release-builder/Dockerfile" "$ROOT"

docker run --rm -it \
    -e CLOUDLESS_SECRET_NAME="$secret_name" \
    -v "$ROOT:/src" \
    -v "$secret_dir:/secrets:ro" \
    -w /src \
    cloudless-release-builder \
    bash -lc '
        set -euo pipefail
        export PATH="/usr/local/go/bin:$PATH"
        export GNUPGHOME="$(mktemp -d)"
        chmod 0700 "$GNUPGHOME"
        export GPG_TTY="$(tty)"
        trap '\''rm -rf "$GNUPGHOME"'\'' EXIT
        gpg --batch --import "/secrets/$CLOUDLESS_SECRET_NAME"
        bash distro/scripts/build-apt-repository.sh '"$VERSION"' '"$CHANNEL"'
    '

echo "Release $VERSION ($CHANNEL) is signed and ready to publish."
