#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fake_bin="$work/bin"
readonly_home="$work/read-only-home"
mkdir -p "$fake_bin" "$readonly_home" "$work/status" "$work/keyrings" "$work/sources"
chmod 0555 "$readonly_home"
chmod 0750 "$work/status"

cat > "$fake_bin/dpkg" <<'EOF'
#!/bin/sh
[ "$1" = "--print-architecture" ] && { printf '%s\n' arm64; exit 0; }
exit 1
EOF
cat > "$fake_bin/curl" <<'EOF'
#!/bin/sh
output=
while [ "$#" -gt 0 ]; do
    if [ "$1" = "-o" ]; then shift; output="$1"; fi
    shift
done
[ -n "$output" ] || exit 1
printf '%s\n' pinned-test-key > "$output"
EOF
cat > "$fake_bin/gpg" <<'EOF'
#!/bin/sh
homedir=
while [ "$#" -gt 0 ]; do
    if [ "$1" = "--homedir" ]; then shift; homedir="$1"; fi
    shift
done
[ -n "$homedir" ] || { echo "test gpg: --homedir was not supplied" >&2; exit 90; }
[ "$homedir" != "$HOME/.gnupg" ] || { echo "test gpg: protected HOME was used" >&2; exit 91; }
[ -d "$homedir" ] && [ "$(stat -c %a "$homedir")" = 700 ] || {
    echo "test gpg: private writable key workspace was not created" >&2
    exit 92
}
[ "${FAIL_GPG:-0}" != 1 ] || exit 93
printf '%s\n' \
    'fpr:::::::::2596A99EAAB33821893C0A79458CA832957F5868:' \
    'fpr:::::::::2F625B3A774B946822EDDBEEB1547A3DDAAF03C6:'
EOF
cat > "$fake_bin/apt-get" <<'EOF'
#!/bin/sh
for argument in "$@"; do
    if [ "$argument" = "install" ]; then
        cat > "$FAKE_BIN/tailscale" <<'INNER'
#!/bin/sh
exit 0
INNER
        chmod 0755 "$FAKE_BIN/tailscale"
        break
    fi
done
exit 0
EOF
cat > "$fake_bin/systemctl" <<'EOF'
#!/bin/sh
exit 0
EOF
cat > "$fake_bin/install" <<'EOF'
#!/usr/bin/env bash
set -e
arguments=()
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o|-g) shift 2 ;;
        *) arguments+=("$1"); shift ;;
    esac
done
exec /usr/bin/install "${arguments[@]}"
EOF
chmod 0755 "$fake_bin"/*

HOME="$readonly_home" \
FAKE_BIN="$fake_bin" \
PATH="$fake_bin:/usr/bin:/bin" \
CLOUDLESS_TAILSCALE_STATUS_DIR="$work/status" \
CLOUDLESS_TAILSCALE_KEYRING="$work/keyrings/tailscale.gpg" \
CLOUDLESS_TAILSCALE_SOURCE="$work/sources/tailscale.list" \
    "$ROOT/distro/packages/cloudless-orchestrator/cloudless-install-tailscale"

grep -Fq '"state":"installed"' "$work/status/tailscale-install.json"
[ "$(stat -c %a "$work/status")" = 750 ]
grep -Fq 'signed-by=' "$work/sources/tailscale.list"
test -s "$work/keyrings/tailscale.gpg"

rm -f "$fake_bin/tailscale" "$work/status/tailscale-install.json"
if HOME="$readonly_home" \
    FAIL_GPG=1 \
    FAKE_BIN="$fake_bin" \
    PATH="$fake_bin:/usr/bin:/bin" \
    CLOUDLESS_TAILSCALE_STATUS_DIR="$work/status" \
    CLOUDLESS_TAILSCALE_KEYRING="$work/keyrings/tailscale.gpg" \
    CLOUDLESS_TAILSCALE_SOURCE="$work/sources/tailscale.list" \
        "$ROOT/distro/packages/cloudless-orchestrator/cloudless-install-tailscale"; then
    echo "Installer unexpectedly succeeded when key verification failed" >&2
    exit 1
fi
grep -Fq '"state":"failed"' "$work/status/tailscale-install.json"
grep -Fq "could not verify Tailscale's signed repository key" "$work/status/tailscale-install.json"
echo "Fresh Tailscale installation succeeds with a protected, read-only home."
