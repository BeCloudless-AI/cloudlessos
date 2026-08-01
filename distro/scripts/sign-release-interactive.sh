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
case "$CHANNEL" in stable|beta) ;; *) echo "Channel must be stable or beta" >&2; exit 2 ;; esac
if [ "$CHANNEL" = stable ] && [ -z "${CLOUDLESS_BETA_PROMOTION:-}" ]; then
    echo "Stable releases must be promoted through distro/scripts/release.sh after the beta soak." >&2
    exit 1
fi
test -s "$SECRET_KEY" || { echo "Signing-key backup not found: $SECRET_KEY" >&2; exit 1; }
command -v docker >/dev/null || { echo "Docker is required." >&2; exit 1; }
command -v git >/dev/null || { echo "Git is required." >&2; exit 1; }

secret_dir="$(dirname "$(realpath "$SECRET_KEY")")"
secret_name="$(basename "$SECRET_KEY")"
source_commit="$(git -C "$ROOT" rev-parse HEAD)"
passphrase_file="${CLOUDLESS_GPG_PASSPHRASE_FILE:-}"
temporary_passphrase=""
if [ -z "$passphrase_file" ]; then
    temporary_passphrase="$(mktemp)"
    chmod 0600 "$temporary_passphrase"
    read -r -s -p "Cloudless archive-key passphrase: " archive_passphrase </dev/tty
    echo >/dev/tty
    [ -n "$archive_passphrase" ] || {
        rm -f "$temporary_passphrase"
        echo "The archive-key passphrase cannot be empty." >&2
        exit 1
    }
    printf '%s' "$archive_passphrase" > "$temporary_passphrase"
    unset archive_passphrase
    passphrase_file="$temporary_passphrase"
else
    passphrase_file="$(realpath "$passphrase_file")"
    test -r "$passphrase_file" || { echo "Archive-key passphrase file is not readable." >&2; exit 1; }
fi
cleanup() {
    [ -z "$temporary_passphrase" ] || rm -f "$temporary_passphrase"
}
trap cleanup EXIT

docker image inspect cloudless-release-builder >/dev/null 2>&1 || \
    docker build -t cloudless-release-builder \
        -f "$ROOT/distro/docker/release-builder/Dockerfile" "$ROOT"

docker run --rm -it \
    -e CLOUDLESS_SECRET_NAME="$secret_name" \
    -e CLOUDLESS_SOURCE_COMMIT="$source_commit" \
    -e CLOUDLESS_BETA_PROMOTION \
    -e CLOUDLESS_GPG_PASSPHRASE_FILE=/run/secrets/cloudless-archive-passphrase \
    -v "$ROOT:/src" \
    -v "$secret_dir:/secrets:ro" \
    -v "$passphrase_file:/run/secrets/cloudless-archive-passphrase:ro" \
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
        fingerprint="$(tr -d "[:space:]" < distro/release/keys/cloudless-archive-fingerprint.txt)"
        probe="$(mktemp)"
        printf "CloudlessOS signing probe\n" > "$probe"
        gpg --batch --yes --pinentry-mode loopback \
            --passphrase-file "$CLOUDLESS_GPG_PASSPHRASE_FILE" \
            --local-user "$fingerprint" --detach-sign --output "$probe.sig" "$probe"
        rm -f "$probe" "$probe.sig"
        bash distro/scripts/build-apt-repository.sh '"$VERSION"' '"$CHANNEL"'
    '

echo "Release $VERSION ($CHANNEL) is signed and ready to publish."
