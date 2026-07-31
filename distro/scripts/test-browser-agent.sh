#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
AGENT="$ROOT/distro/packages/cloudless-shell/cloudless-browser-agent"
for command in flock python3; do
  command -v "$command" >/dev/null || {
    echo "Missing browser-agent test command: $command" >&2
    exit 1
  }
done

work="$(mktemp -d)"
agent_pid=""
cleanup() {
  if [[ -n "$agent_pid" ]]; then
    kill "$agent_pid" >/dev/null 2>&1 || true
    wait "$agent_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$work/bin" "$work/runtime/requests" "$work/home"
fake_browser="$work/bin/chromium"
invocations="$work/invocations"
cat >"$fake_browser" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>"$CLOUDLESS_BROWSER_TEST_INVOCATIONS"
sleep "${CLOUDLESS_BROWSER_TEST_DELAY:-0}"
SH
chmod 0755 "$fake_browser"

start_agent() {
  HOME="$work/home" \
  CLOUDLESS_BROWSER_REQUESTS="$work/runtime/requests" \
  CLOUDLESS_BROWSER_STATUS="$work/runtime/status.json" \
  CLOUDLESS_BROWSER_LOCK="$work/runtime/agent.lock" \
  CLOUDLESS_BROWSER_LOCK_WAIT_SECONDS=1 \
  CLOUDLESS_BROWSER_POLL_SECONDS=0.02 \
  CLOUDLESS_BROWSER_BIN="$fake_browser" \
  CLOUDLESS_BROWSER_TEST_INVOCATIONS="$invocations" \
    "$AGENT" >"$work/agent.log" 2>&1 &
  agent_pid=$!
}

wait_for() {
  local description="$1"
  shift
  for _ in $(seq 1 150); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 0.02
  done
  echo "Timed out waiting for $description" >&2
  cat "$work/agent.log" >&2 || true
  return 1
}

requests_empty() {
  ! find "$work/runtime/requests" -maxdepth 1 -type f -print -quit | grep -q .
}

request_one="$work/runtime/requests/one.json"
printf '%s\n' '{"action":"open","url":"https://example.com/one"}' >"$request_one"
chmod 0000 "$fake_browser"
start_agent
sleep 0.15
test -f "$request_one" || {
  echo "Browser request was discarded while the browser was unavailable" >&2
  exit 1
}
chmod 0755 "$fake_browser"
wait_for "first browser dispatch" grep -Fq 'https://example.com/one' "$invocations"
wait_for "first request acknowledgement" test ! -e "$request_one"
test "$(stat -c '%a' "$work/runtime/status.json")" = 660
grep -Fq -- "--user-data-dir=$work/home/.config/cloudless-web-browser" "$invocations"

# A second autostart evaluation must exit without consuming the active lock.
HOME="$work/home" \
CLOUDLESS_BROWSER_REQUESTS="$work/runtime/requests" \
CLOUDLESS_BROWSER_STATUS="$work/runtime/status.json" \
CLOUDLESS_BROWSER_LOCK="$work/runtime/agent.lock" \
CLOUDLESS_BROWSER_LOCK_WAIT_SECONDS=0.1 \
CLOUDLESS_BROWSER_POLL_SECONDS=0.02 \
CLOUDLESS_BROWSER_BIN="$fake_browser" \
CLOUDLESS_BROWSER_TEST_INVOCATIONS="$invocations" \
  "$AGENT" >"$work/second-agent.log" 2>&1 &
second_pid=$!
wait "$second_pid"
kill -0 "$agent_pid"

kill "$agent_pid"
wait "$agent_pid" || true
agent_pid=""
request_two="$work/runtime/requests/two.json"
printf '%s\n' '{"action":"open","url":"https://example.com/two"}' >"$request_two"
start_agent
wait_for "post-restart browser dispatch" grep -Fq 'https://example.com/two' "$invocations"
wait_for "post-restart request acknowledgement" test ! -e "$request_two"
if [[ "$(grep -Fc -- "--user-data-dir=$work/home/.config/cloudless-web-browser" "$invocations")" -ne 2 ]]; then
  echo "Browser restart did not reuse the persistent Cloudless profile" >&2
  exit 1
fi

invalid="$work/runtime/requests/invalid.json"
printf '%s\n' '{"action":"open","url":"file:///etc/passwd"}' >"$invalid"
wait_for "invalid request rejection" test ! -e "$invalid"
if grep -Fq 'file:///etc/passwd' "$invocations"; then
  echo "Browser agent dispatched an unsafe URL" >&2
  exit 1
fi

# Exercise a burst with a process restart in the middle. Delivery is
# intentionally at-least-once across a crash: no safe browser protocol can
# atomically prove both external launch and request deletion, but requests must
# never disappear. Reusing the same profile remains mandatory.
export CLOUDLESS_BROWSER_TEST_DELAY=0.03
for number in $(seq 1 24); do
  printf '{"action":"open","url":"https://example.com/burst/%s"}\n' "$number" \
    >"$work/runtime/requests/burst-$number.json"
done
sleep 0.08
kill "$agent_pid"
wait "$agent_pid" || true
agent_pid=""
start_agent
wait_for "burst request completion after restart" requests_empty
for number in $(seq 1 24); do
  if ! grep -Fq "https://example.com/burst/$number" "$invocations"; then
    echo "Browser request burst lost request $number across restart" >&2
    exit 1
  fi
done
if ! grep -Fq -- "--user-data-dir=$work/home/.config/cloudless-web-browser" "$invocations"; then
  echo "Browser burst did not retain the persistent profile" >&2
  exit 1
fi

echo "Browser request retry, burst recovery, locking, restart and profile persistence passed."
