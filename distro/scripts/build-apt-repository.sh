#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
VERSION="${1:-}"
CHANNEL="${2:-stable}"
REPO="${CLOUDLESS_APT_REPO_OUT:-$DISTRO/out/apt-repository}"
KEY="$DISTRO/release/keys/cloudless-archive-keyring.pgp"
FINGERPRINT_FILE="$DISTRO/release/keys/cloudless-archive-fingerprint.txt"

if [ -z "$VERSION" ] || [[ "$VERSION" == *dev* ]]; then
    echo "Usage: $0 VERSION [stable|beta] (development versions cannot be published)" >&2
    exit 2
fi
case "$CHANNEL" in stable|beta) ;; *) echo "Channel must be stable or beta" >&2; exit 2 ;; esac
for command in gpg reprepro; do command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 1; }; done
test -s "$KEY" || { echo "Initialize the archive signing key first." >&2; exit 1; }
test -s "$FINGERPRINT_FILE" || { echo "Missing archive fingerprint file." >&2; exit 1; }
fingerprint="$(tr -d '[:space:]' < "$FINGERPRINT_FILE")"
gpg --batch --list-secret-keys "$fingerprint" >/dev/null 2>&1 || {
    echo "The secret signing key is not loaded in GNUPGHOME." >&2
    exit 1
}

CLOUDLESS_VERSION="$VERSION" "$DISTRO/scripts/build-packages.sh"
install -d "$REPO/conf" "$REPO/releases"
cat > "$REPO/conf/distributions" <<EOF
Origin: Cloudless
Label: CloudlessOS
Codename: $CHANNEL
Suite: $CHANNEL
Architectures: amd64
Components: main
Description: Signed CloudlessOS $CHANNEL updates
SignWith: $fingerprint
EOF
cat > "$REPO/conf/options" <<'EOF'
verbose
ask-passphrase
EOF
for deb in "$DISTRO"/out/packages/cloudless-*.deb; do
    reprepro --basedir "$REPO" includedeb "$CHANNEL" "$deb"
done
cp "$KEY" "$REPO/cloudless-archive-keyring.pgp"
cat > "$REPO/releases/$VERSION.json" <<EOF
{"version":"$VERSION","channel":"$CHANNEL","publishedAt":"$(date -u +%FT%TZ)"}
EOF
echo "Signed APT repository ready at $REPO"
