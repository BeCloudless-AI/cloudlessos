#!/usr/bin/env bash
set -euo pipefail

# This test replaces X11 programs and opens fixed local ports. It must only run
# inside the disposable package-lifecycle container.
[[ "${CLOUDLESS_DISPOSABLE_PACKAGE_TEST:-}" == "1" ]] || {
  echo "Refusing to run the installed-session rehearsal outside its disposable container." >&2
  exit 2
}

for command in python3 runuser install stat; do
  command -v "$command" >/dev/null || {
    echo "Missing installed-session test command: $command" >&2
    exit 1
  }
done
for binary in \
  /usr/lib/cloudless/cloudlessd \
  /usr/bin/cloudless-desktop-agent \
  /usr/bin/cloudless-browser-agent; do
  [[ -x "$binary" ]] || {
    echo "Missing installed-session package payload: $binary" >&2
    exit 1
  }
done

work="$(mktemp -d)"
chmod 0755 "$work"
daemon_pid=
desktop_pid=
browser_pid=
terminal_pid=
broker_pid=
cleanup() {
  for pid in "$daemon_pid" "$desktop_pid" "$browser_pid" "$terminal_pid" "$broker_pid"; do
    [[ -n "$pid" ]] || continue
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$work"
}
trap cleanup EXIT

install -d -o cloudlessd -g cloudless-control -m 0750 "$work/state"
install -d -o cloudless -g cloudless -m 0750 "$work/home" "$work/home/.config"
install -d -o root -g cloudless -m 0770 \
  /run/cloudless-desktop /run/cloudless-browser /run/cloudless-browser/requests
install -o cloudless -g cloudless -m 0660 /dev/null /run/cloudless-browser/status.json
printf '{"running":false,"minimized":false}\n' >/run/cloudless-browser/status.json

cat >"$work/xrandr" <<'SH'
#!/bin/sh
if [ "${1:-}" = "--query" ]; then
  cat <<'EOF'
Screen 0: minimum 320 x 200, current 1920 x 1080, maximum 16384 x 16384
DP-0 connected primary 1920x1080+0+0 (normal left inverted right x axis y axis)
   1920x1080     60.00*+
   1280x720      60.00
EOF
  exit 0
fi
printf 'xrandr %s\n' "$*" >>"${CLOUDLESS_SESSION_ACTIONS:?}"
SH
cat >"$work/xdotool" <<'SH'
#!/bin/sh
printf 'xdotool %s\n' "$*" >>"${CLOUDLESS_SESSION_ACTIONS:?}"
SH
chmod 0755 "$work/xrandr" "$work/xdotool"
install -m 0755 "$work/xrandr" /usr/bin/xrandr
install -m 0755 "$work/xdotool" /usr/bin/xdotool
install -o cloudless -g cloudless -m 0660 /dev/null "$work/actions.log"

cat >"$work/chromium" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>"${CLOUDLESS_BROWSER_INVOCATIONS:?}"
SH
chmod 0755 "$work/chromium"
chown cloudless:cloudless "$work/chromium"
install -o cloudless -g cloudless -m 0660 /dev/null "$work/browser.log"

cat >"$work/broker.py" <<'PY'
import json
import os
import socket
import sys

path, log_path = sys.argv[1:]
try:
    os.unlink(path)
except FileNotFoundError:
    pass
server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
server.bind(path)
os.chmod(path, 0o666)
server.listen(8)
while True:
    client, _ = server.accept()
    with client:
        request = json.loads(client.makefile("rb").readline())
        with open(log_path, "a", encoding="utf-8") as handle:
            handle.write(request.get("action", "") + "|" + request.get("value", "") + "\n")
        client.sendall(b'{"ok":true}\n')
PY
install -m 0666 /dev/null "$work/privileged.log"
python3 "$work/broker.py" "$work/privileged.sock" "$work/privileged.log" \
  >/tmp/cloudless-session-broker.log 2>&1 &
broker_pid=$!
for _ in $(seq 1 100); do
  [[ -S "$work/privileged.sock" ]] && break
  sleep 0.05
done
[[ -S "$work/privileged.sock" ]] || {
  cat /tmp/cloudless-session-broker.log >&2
  exit 1
}

cat >"$work/terminal.py" <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer
import os

nonce = str(os.getpid())

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = ("terminal-session=" + nonce + "\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *_):
        pass

HTTPServer(("127.0.0.1", 7681), Handler).serve_forever()
PY

cat >"$work/request.py" <<'PY'
import json
import sys
import urllib.request

method, path = sys.argv[1:3]
payload = None if len(sys.argv) < 4 else sys.argv[3].encode()
request = urllib.request.Request("http://127.0.0.1:18765" + path, data=payload, method=method)
if len(sys.argv) >= 5:
    request.add_header("X-Cloudless-Action", sys.argv[4])
if payload is not None:
    request.add_header("Content-Type", "application/json")
with urllib.request.urlopen(request, timeout=4) as response:
    print(response.read().decode())
PY

runuser -u cloudless -- env \
  HOME="$work/home" \
  CLOUDLESS_SESSION_ACTIONS="$work/actions.log" \
  /usr/bin/cloudless-desktop-agent >/tmp/cloudless-session-desktop.log 2>&1 &
desktop_pid=$!

runuser -u cloudless -- env \
  HOME="$work/home" \
  XDG_CONFIG_HOME="$work/home/.config" \
  CLOUDLESS_BROWSER_BIN="$work/chromium" \
  CLOUDLESS_BROWSER_INVOCATIONS="$work/browser.log" \
  CLOUDLESS_BROWSER_POLL_SECONDS=0.05 \
  /usr/bin/cloudless-browser-agent >/tmp/cloudless-session-browser.log 2>&1 &
browser_pid=$!

runuser -u cloudless -- python3 "$work/terminal.py" >/tmp/cloudless-session-terminal.log 2>&1 &
terminal_pid=$!

start_daemon() {
  runuser -u cloudlessd -- env \
    CLOUDLESS_ADDR=127.0.0.1:18765 \
    CLOUDLESS_GATEWAY_ADDR=127.0.0.1:18766 \
    CLOUDLESS_STATE_DIR="$work/state" \
    CLOUDLESS_PRIVILEGED_SOCKET="$work/privileged.sock" \
    TZ=Etc/UTC \
    CLOUDLESS_NO_PROVISION=1 \
    /usr/lib/cloudless/cloudlessd >/tmp/cloudless-session-daemon.log 2>&1 &
  daemon_pid=$!
}

wait_http() {
  local path="$1"
  for _ in $(seq 1 100); do
    if python3 "$work/request.py" GET "$path" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.05
  done
  cat /tmp/cloudless-session-daemon.log >&2 || true
  return 1
}

wait_file_contains() {
  local file="$1" expected="$2"
  for _ in $(seq 1 100); do
    if grep -Fq -- "$expected" "$file" 2>/dev/null; then
      return 0
    fi
    sleep 0.05
  done
  echo "Timed out waiting for '$expected' in $file" >&2
  return 1
}

for _ in $(seq 1 100); do
  [[ -S /run/cloudless-desktop/agent.sock ]] && break
  sleep 0.05
done
[[ -S /run/cloudless-desktop/agent.sock ]] || {
  cat /tmp/cloudless-session-desktop.log >&2
  exit 1
}
[[ "$(stat -c '%U:%G:%a' /run/cloudless-desktop/agent.sock)" == "cloudless:cloudless:660" ]]

# Root possesses more host privilege but is not the authenticated desktop
# client. The session agent must still reject it.
root_reply="$(python3 - /run/cloudless-desktop/agent.sock <<'PY'
import json, socket, sys
client = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
client.connect(sys.argv[1])
print(json.loads(client.makefile("rb").readline()).get("error", ""))
PY
)"
[[ "$root_reply" == "desktop client is not authorized" ]]

start_daemon
wait_http /api/health
display="$(python3 "$work/request.py" GET /api/system/display)"
grep -Fq '"available":true' <<<"$display"
grep -Fq '"name":"DP-0"' <<<"$display"

python3 "$work/request.py" POST /api/system/input/key \
  '{"action":"text","value":"A"}' virtual-keyboard >/dev/null
wait_file_contains "$work/actions.log" "xdotool type --clearmodifiers --delay 0 A"

python3 "$work/request.py" POST /api/system/browser \
  '{"url":"https://example.com/qualification","action":"open"}' >/dev/null
wait_file_contains "$work/browser.log" "https://example.com/qualification"
grep -Fq -- "--user-data-dir=$work/home/.config/cloudless-web-browser" "$work/browser.log"

terminal_before="$(python3 "$work/request.py" GET /terminal/)"
grep -Eq '^terminal-session=[0-9]+$' <<<"$terminal_before"

# Leave a display change unconfirmed, terminate the daemon and prove that the
# restarted installed daemon asks the same desktop agent to restore it.
display_change="$(python3 "$work/request.py" POST /api/system/display \
  '{"output":"DP-0","width":1280,"height":720}' display)"
grep -Fq '"confirmationRequired":true' <<<"$display_change"
wait_file_contains "$work/actions.log" "xrandr --output DP-0 --mode 1280x720"
kill "$daemon_pid"
wait "$daemon_pid" || true
daemon_pid=
start_daemon
wait_http /api/health
wait_file_contains "$work/actions.log" "xrandr --output DP-0 --mode 1920x1080"
[[ ! -e "$work/state/display-change-pending.json" ]]

# Apply the same mode again and explicitly confirm it. The confirmed preference
# must survive independently from the transient desktop-agent connection.
confirmed_change="$(python3 "$work/request.py" POST /api/system/display \
  '{"output":"DP-0","width":1280,"height":720}' display)"
confirmation_token="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])' <<<"$confirmed_change")"
python3 "$work/request.py" POST /api/system/display/confirm \
  "{\"token\":\"$confirmation_token\"}" display-confirm >/dev/null
grep -Eq '"width":[[:space:]]*1280' "$work/state/state.json"
grep -Eq '"height":[[:space:]]*720' "$work/state/state.json"

# Region selection crosses the real daemon-to-broker protocol. The disposable
# broker records the requested IANA zone without changing its host container.
profile="$(python3 "$work/request.py" POST /api/profile \
  '{"name":"Qualification","region":"AE"}')"
grep -Fq '"region":"AE"' <<<"$profile"
grep -Fq '"country":"AE"' <<<"$profile"
wait_file_contains "$work/privileged.log" "timezone.set|Asia/Dubai"

# Browser profile and terminal process are graphical-session resources, not
# daemon children. Both remain the same after cloudlessd restarts.
python3 "$work/request.py" POST /api/system/browser \
  '{"url":"https://example.com/after-restart","action":"open"}' >/dev/null
wait_file_contains "$work/browser.log" "https://example.com/after-restart"
[[ "$(grep -Fc -- "--user-data-dir=$work/home/.config/cloudless-web-browser" "$work/browser.log")" -eq 2 ]]
terminal_after="$(python3 "$work/request.py" GET /terminal/)"
[[ "$terminal_after" == "$terminal_before" ]]

# Exercise both destructive UI routes against the broker without powering off
# the disposable qualification container.
python3 "$work/request.py" POST /api/system/reboot '{}' reboot >/dev/null
python3 "$work/request.py" POST /api/system/shutdown '{}' shutdown >/dev/null
wait_file_contains "$work/privileged.log" "power.restart|"
wait_file_contains "$work/privileged.log" "power.shutdown|"

echo "Installed display, locale, power, keyboard, browser and terminal integration passed."
