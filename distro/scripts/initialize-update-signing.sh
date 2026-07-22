#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PUBLIC_DIR="$ROOT/distro/release/keys"
PUBLIC_KEY="$PUBLIC_DIR/cloudless-archive-keyring.pgp"
FINGERPRINT_FILE="$PUBLIC_DIR/cloudless-archive-fingerprint.txt"
IDENTITY="${CLOUDLESS_SIGNING_IDENTITY:-CloudlessOS Archive <updates@becloudless.ai>}"
SECRET_OUTPUT="${1:-}"

if [ -z "$SECRET_OUTPUT" ]; then
    echo "Usage: $0 /absolute/path/outside-the-repository/cloudless-archive-secret.asc" >&2
    exit 2
fi
case "$(realpath -m "$SECRET_OUTPUT")" in
    "$ROOT"/*) echo "The private key backup must be outside the repository." >&2; exit 2 ;;
esac
command -v gpg >/dev/null || { echo "Install GnuPG first: sudo apt install gnupg" >&2; exit 1; }
if [ -e "$PUBLIC_KEY" ]; then
    echo "$PUBLIC_KEY already exists; refusing to replace the production archive identity." >&2
    exit 1
fi
if [ -e "$SECRET_OUTPUT" ]; then
    echo "$SECRET_OUTPUT already exists; refusing to overwrite the private-key backup." >&2
    exit 1
fi

read -r -s -p "New archive-key passphrase: " passphrase; echo
read -r -s -p "Repeat passphrase: " confirmation; echo
if [ -z "$passphrase" ] || [ "$passphrase" != "$confirmation" ]; then
    echo "Passphrases were empty or did not match." >&2
    exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"; unset passphrase confirmation' EXIT
export GNUPGHOME="$work/gnupg"
install -d -m 0700 "$GNUPGHOME"
mkdir -p "$(dirname "$SECRET_OUTPUT")" "$PUBLIC_DIR"
printf '%s' "$passphrase" | gpg --batch --pinentry-mode loopback --passphrase-fd 0 \
    --quick-generate-key "$IDENTITY" ed25519 cert,sign 2y
fingerprint="$(gpg --batch --with-colons --list-keys "$IDENTITY" | awk -F: '$1 == "fpr" {print $10; exit}')"
test -n "$fingerprint"
gpg --batch --export "$fingerprint" > "$work/public.pgp"
printf '%s' "$passphrase" | gpg --batch --pinentry-mode loopback --passphrase-fd 0 \
    --armor --export-secret-keys "$fingerprint" > "$work/secret.asc"
printf '%s\n' "$fingerprint" > "$work/fingerprint.txt"
test -s "$work/public.pgp"
test -s "$work/secret.asc"
gpg --batch --show-keys --with-colons "$work/public.pgp" | grep -Fq "fpr:::::::::$fingerprint:"
install -m 0644 "$work/public.pgp" "$PUBLIC_KEY"
install -m 0644 "$work/fingerprint.txt" "$FINGERPRINT_FILE"
install -m 0600 "$work/secret.asc" "$SECRET_OUTPUT"

echo "CloudlessOS archive identity created."
echo "Fingerprint: $fingerprint"
echo "Public key: $PUBLIC_KEY (commit this file)"
echo "Encrypted private backup: $SECRET_OUTPUT (store offline; never commit it)"
