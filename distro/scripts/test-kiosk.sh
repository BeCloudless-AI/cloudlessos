#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KIOSK="$ROOT/distro/packages/cloudless-shell/cloudless-kiosk"

sh -n "$KIOSK"
source <(sed -n '/^firefox_profile_running()/,/^}/p' "$KIOSK")

test_profile="/tmp/cloudless-kiosk-profile-test-$$"
bash -c 'exec -a firefox bash -c "while :; do sleep 1; done" --profile "$1"' _ "$test_profile" &
test_pid=$!
cleanup() {
    kill "$test_pid" >/dev/null 2>&1 || true
    wait "$test_pid" 2>/dev/null || true
}
trap cleanup EXIT

for _ in $(seq 1 20); do
    firefox_profile_running "$test_profile" && break
    sleep 0.05
done
firefox_profile_running "$test_profile" || {
    echo "kiosk supervisor did not recognize the live Firefox profile" >&2
    exit 1
}
if firefox_profile_running "$test_profile-other"; then
    echo "kiosk supervisor confused two Firefox profiles" >&2
    exit 1
fi

dgx_line="$(grep -n 'if is_dgx_spark; then' "$KIOSK" | head -n1 | cut -d: -f1)"
nvidia_line="$(grep -n 'elif command -v nvidia-smi' "$KIOSK" | head -n1 | cut -d: -f1)"
test -n "$dgx_line" && test -n "$nvidia_line" && test "$dgx_line" -lt "$nvidia_line" || {
    echo "DGX software rendering must take precedence over generic NVIDIA acceleration" >&2
    exit 1
}
grep -Fq 'LIBGL_ALWAYS_SOFTWARE=1 MOZ_WEBRENDER=0' "$KIOSK"
grep -Fq 'recovered Firefox already owns the kiosk profile' "$KIOSK"

echo "Kiosk crash-recovery safeguards passed"
