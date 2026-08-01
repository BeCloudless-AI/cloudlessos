#!/usr/bin/env bash
set -euo pipefail

for command in docker mktemp; do
    command -v "$command" >/dev/null || { echo "Missing protected-signing test command: $command" >&2; exit 1; }
done
docker image inspect cloudless-release-builder >/dev/null 2>&1 || {
    echo "The cloudless-release-builder image is required." >&2
    exit 1
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
chmod 0700 "$work"

# Create a passphrase-protected disposable archive key. The passphrase exists
# only in this private test directory and is never passed through an environment
# variable or command-line argument.
docker run --rm -v "$work:/test" cloudless-release-builder bash -lc '
    set -euo pipefail
    export GNUPGHOME=/test/generate-gnupg
    install -d -m 0700 "$GNUPGHOME"
    printf "%s" "protected-signing-test-only" > /test/passphrase
    chmod 0600 /test/passphrase
    gpg --batch --pinentry-mode loopback --passphrase-file /test/passphrase \
        --quick-generate-key "Cloudless Protected Signing Test <test@invalid>" ed25519 sign 1d
    fingerprint="$(gpg --batch --with-colons --list-keys | awk -F: '\''$1 == "fpr" {print $10; exit}'\'')"
    gpg --batch --pinentry-mode loopback --passphrase-file /test/passphrase \
        --armor --export-secret-keys "$fingerprint" > /test/archive-secret.asc
    gpg --batch --export "$fingerprint" > /test/archive-keyring.pgp
    rm -rf "$GNUPGHOME"
'

# Reproduce the production container boundary: import the encrypted key from
# one read-only mount, read the passphrase from another, sign without a TTY,
# and independently verify the result.
docker run --rm \
    -e CLOUDLESS_GPG_PASSPHRASE_FILE=/run/secrets/cloudless-archive-passphrase \
    -v "$work/archive-secret.asc:/secrets/archive-secret.asc:ro" \
    -v "$work/passphrase:/run/secrets/cloudless-archive-passphrase:ro" \
    -v "$work/archive-keyring.pgp:/test/archive-keyring.pgp:ro" \
    cloudless-release-builder bash -lc '
        set -euo pipefail
        export GNUPGHOME="$(mktemp -d)"
        trap '\''rm -rf "$GNUPGHOME" /tmp/cloudless-signing-probe*'\'' EXIT
        chmod 0700 "$GNUPGHOME"
        gpg --batch --import /secrets/archive-secret.asc
        fingerprint="$(gpg --batch --with-colons --list-secret-keys | awk -F: '\''$1 == "fpr" {print $10; exit}'\'')"
        printf "CloudlessOS protected signing probe\n" > /tmp/cloudless-signing-probe
        gpg --batch --yes --pinentry-mode loopback \
            --passphrase-file "$CLOUDLESS_GPG_PASSPHRASE_FILE" \
            --local-user "$fingerprint" --detach-sign \
            --output /tmp/cloudless-signing-probe.sig /tmp/cloudless-signing-probe
        gpgv --keyring /test/archive-keyring.pgp \
            /tmp/cloudless-signing-probe.sig /tmp/cloudless-signing-probe
    '

echo "Protected signing works with an encrypted key, read-only secret mounts, and no container TTY."
