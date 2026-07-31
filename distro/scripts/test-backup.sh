#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT/distro/packages/cloudless-orchestrator/cloudless-backup"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$WORK/system/var/lib/cloudless/secrets" "$WORK/system/etc/cloudless"
printf 'original\n' > "$WORK/system/var/lib/cloudless/state.json"
printf 'private\n' > "$WORK/system/var/lib/cloudless/secrets/key"
printf 'config\n' > "$WORK/system/etc/cloudless/cloudless.env"
printf 'correct horse battery staple\n' > "$WORK/passphrase"
chmod 0600 "$WORK/passphrase"

export CLOUDLESS_BACKUP_ROOT="$WORK/system"
export CLOUDLESS_BACKUP_PASSPHRASE_FILE="$WORK/passphrase"
"$SCRIPT" create "$WORK/control-plane.cloudless-backup"
"$SCRIPT" verify "$WORK/control-plane.cloudless-backup"

printf 'damaged\n' > "$WORK/system/var/lib/cloudless/state.json"
"$SCRIPT" restore "$WORK/control-plane.cloudless-backup" --yes
grep -qx original "$WORK/system/var/lib/cloudless/state.json"
grep -qx private "$WORK/system/var/lib/cloudless/secrets/key"
test -d "$WORK/system/var/backups/cloudless"

cp "$WORK/control-plane.cloudless-backup" "$WORK/corrupt.cloudless-backup"
printf x | dd of="$WORK/corrupt.cloudless-backup" bs=1 seek=20 conv=notrunc status=none
if "$SCRIPT" verify "$WORK/corrupt.cloudless-backup" >/dev/null 2>&1; then
  echo "corrupted backup unexpectedly verified" >&2
  exit 1
fi

mkdir -p "$WORK/malicious/root/var/lib/cloudless" "$WORK/malicious/envelope"
ln -s /etc "$WORK/malicious/root/var/lib/cloudless/escape"
tar -czf "$WORK/malicious/envelope/payload.tar.gz" -C "$WORK/malicious/root" var/lib/cloudless
(cd "$WORK/malicious/envelope" && sha256sum payload.tar.gz > SHA256SUMS)
(cd "$WORK/malicious/envelope" && tar -czf - payload.tar.gz SHA256SUMS) |
  gpg --batch --quiet --yes --symmetric --cipher-algo AES256 \
    --pinentry-mode loopback --passphrase-file "$WORK/passphrase" \
    --output "$WORK/malicious.cloudless-backup"
if "$SCRIPT" verify "$WORK/malicious.cloudless-backup" >/dev/null 2>&1; then
  echo "symlink-bearing backup unexpectedly verified" >&2
  exit 1
fi

# Creation validates the source payload before publishing the final filename.
# A symlink in protected state must fail without stranding a corrupt backup.
mkdir -p "$WORK/unsafe/var/lib/cloudless"
ln -s /etc "$WORK/unsafe/var/lib/cloudless/escape"
if CLOUDLESS_BACKUP_ROOT="$WORK/unsafe" "$SCRIPT" create "$WORK/unsafe.cloudless-backup" >/dev/null 2>&1; then
  echo "unsafe source unexpectedly produced a backup" >&2
  exit 1
fi
test ! -e "$WORK/unsafe.cloudless-backup"
if find "$WORK" -maxdepth 1 -name '.cloudless-backup.*' | grep -q .; then
  echo "failed backup left a temporary output behind" >&2
  exit 1
fi

# Restore is an exact control-plane snapshot. A path absent from the backup
# removes a newer stale path while retaining it in the collision-safe rollback.
mkdir -p "$WORK/minimal/var/lib/cloudless"
printf 'minimal\n' > "$WORK/minimal/var/lib/cloudless/state.json"
CLOUDLESS_BACKUP_ROOT="$WORK/minimal" "$SCRIPT" create "$WORK/minimal.cloudless-backup"
mkdir -p "$WORK/minimal/etc/cloudless"
printf 'stale\n' > "$WORK/minimal/etc/cloudless/stale.conf"
CLOUDLESS_BACKUP_ROOT="$WORK/minimal" "$SCRIPT" restore "$WORK/minimal.cloudless-backup" --yes
test ! -e "$WORK/minimal/etc/cloudless"
first_rollback="$(find "$WORK/minimal/var/backups/cloudless" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
grep -qx stale "$first_rollback/etc/cloudless/stale.conf"

# Two restores in the same second must never reuse or overwrite rollback state.
mkdir -p "$WORK/minimal/etc/cloudless"
printf 'second\n' > "$WORK/minimal/etc/cloudless/stale.conf"
CLOUDLESS_BACKUP_ROOT="$WORK/minimal" "$SCRIPT" restore "$WORK/minimal.cloudless-backup" --yes
test "$(find "$WORK/minimal/var/backups/cloudless" -mindepth 1 -maxdepth 1 -type d | wc -l)" -eq 2

# The operation lock is fail-closed and scoped to the selected machine root.
mkdir -p "$WORK/locked/run/lock" "$WORK/locked/var/lib/cloudless"
printf 'locked\n' > "$WORK/locked/var/lib/cloudless/state.json"
(
  exec 9>"$WORK/locked/run/lock/cloudless-backup.lock"
  flock 9
  printf ready > "$WORK/lock-ready"
  sleep 5
) &
locker=$!
trap 'kill "$locker" 2>/dev/null || true; rm -rf "$WORK"' EXIT
while [[ ! -e "$WORK/lock-ready" ]]; do sleep 0.05; done
if CLOUDLESS_BACKUP_ROOT="$WORK/locked" "$SCRIPT" create "$WORK/locked.cloudless-backup" >"$WORK/locked.err" 2>&1; then
  echo "overlapping backup unexpectedly acquired the operation lock" >&2
  exit 1
fi
grep -q 'another Cloudless backup or restore is already running' "$WORK/locked.err"
kill "$locker" 2>/dev/null || true
wait "$locker" 2>/dev/null || true
trap 'rm -rf "$WORK"' EXIT

# Control-plane identities are architecture-specific. A valid ARM64 archive
# must not replace AMD64 state (or vice versa), even with the right passphrase.
mkdir -p "$WORK/cross-arch/var/lib/cloudless"
printf 'arm-state\n' > "$WORK/cross-arch/var/lib/cloudless/state.json"
CLOUDLESS_BACKUP_ROOT="$WORK/cross-arch" CLOUDLESS_BACKUP_TEST_ARCH=arm64 \
  "$SCRIPT" create "$WORK/cross-arch.cloudless-backup"
printf 'amd-state\n' > "$WORK/cross-arch/var/lib/cloudless/state.json"
if CLOUDLESS_BACKUP_ROOT="$WORK/cross-arch" CLOUDLESS_BACKUP_TEST_ARCH=amd64 \
  "$SCRIPT" restore "$WORK/cross-arch.cloudless-backup" --yes >"$WORK/cross-arch.err" 2>&1; then
  echo "cross-architecture restore unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'does not match this machine' "$WORK/cross-arch.err"
grep -qx amd-state "$WORK/cross-arch/var/lib/cloudless/state.json"
echo "Cloudless backup create/verify/restore tests passed."
