#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
AUDIT="$ROOT/distro/packages/cloudless-firstboot/cloudless-boot-audit"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

ready_fixture() {
    local dir="$1"
    mkdir -p "$dir"
    for probe in default-target lightdm x-display graphical-session kiosk-browser browser-profile orchestrator; do
        printf 'ready\n' > "$dir/$probe"
    done
}

ready_fixture "$TMP/ready"
CLOUDLESS_BOOT_AUDIT_STATE_DIR="$TMP/state" \
CLOUDLESS_BOOT_AUDIT_PROBE_DIR="$TMP/ready" CLOUDLESS_BOOT_ID=boot-one \
CLOUDLESS_BOOT_AUDIT_TIMEOUT=0 "$AUDIT"
grep -Fq '"healthy":true' "$TMP/state/boot-health.json"
grep -Fq '"consecutiveHealthyBoots":1' "$TMP/state/boot-health.json"

# Re-running in one boot is idempotent; a new healthy boot increments exactly once.
CLOUDLESS_BOOT_AUDIT_STATE_DIR="$TMP/state" \
CLOUDLESS_BOOT_AUDIT_PROBE_DIR="$TMP/ready" CLOUDLESS_BOOT_ID=boot-one \
CLOUDLESS_BOOT_AUDIT_TIMEOUT=0 "$AUDIT"
grep -Fq '"consecutiveHealthyBoots":1' "$TMP/state/boot-health.json"
CLOUDLESS_BOOT_AUDIT_STATE_DIR="$TMP/state" \
CLOUDLESS_BOOT_AUDIT_PROBE_DIR="$TMP/ready" CLOUDLESS_BOOT_ID=boot-two \
CLOUDLESS_BOOT_AUDIT_TIMEOUT=0 "$AUDIT"
grep -Fq '"consecutiveHealthyBoots":2' "$TMP/state/boot-health.json"

printf 'waiting\n' > "$TMP/ready/kiosk-browser"
if CLOUDLESS_BOOT_AUDIT_STATE_DIR="$TMP/state" \
   CLOUDLESS_BOOT_AUDIT_PROBE_DIR="$TMP/ready" CLOUDLESS_BOOT_ID=boot-three \
   CLOUDLESS_BOOT_AUDIT_TIMEOUT=0 "$AUDIT"; then
    echo "Boot audit accepted a missing kiosk browser" >&2
    exit 1
fi
grep -Fq '"healthy":false' "$TMP/state/boot-health.json"
grep -Fq '"consecutiveHealthyBoots":0' "$TMP/state/boot-health.json"
grep -Fq '"reason":"kiosk-browser-missing"' "$TMP/state/boot-health.json"

# Repairing the same boot creates the first healthy observation; it must not
# remain at zero merely because the failed observation used the same boot ID.
printf 'ready\n' > "$TMP/ready/kiosk-browser"
CLOUDLESS_BOOT_AUDIT_STATE_DIR="$TMP/state" \
CLOUDLESS_BOOT_AUDIT_PROBE_DIR="$TMP/ready" CLOUDLESS_BOOT_ID=boot-three \
CLOUDLESS_BOOT_AUDIT_TIMEOUT=0 "$AUDIT"
grep -Fq '"consecutiveHealthyBoots":1' "$TMP/state/boot-health.json"
test "$(wc -l < "$TMP/state/boot-health-history.tsv")" -eq 5

echo "Boot audit tests passed"
