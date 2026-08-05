#!/usr/bin/env bash
set -Eeuo pipefail
trap 'status=$?; echo "package lifecycle command failed at line ${LINENO}: ${BASH_COMMAND} (exit ${status})" >&2' ERR
if [[ "${CLOUDLESS_TRACE:-0}" == "1" ]]; then
  set -x
fi

if [[ "${1:-}" == "--inside" ]]; then
  baseline="${2:?baseline directory required}"
  candidate="${3:?candidate directory required}"
  expected_old="${4:?baseline version required}"
  expected_new="${5:?candidate version required}"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq adduser python3 >/dev/null

  install_generation() {
    local directory="$1" expected="$2"
    dpkg --force-depends --force-confold --force-downgrade -i "$directory"/cloudless-*.deb >/tmp/dpkg.log 2>&1 || {
      cat /tmp/dpkg.log >&2
      return 1
    }
    local package version
    for package in cloudless-orchestrator cloudless-shell cloudless-branding cloudless-hardware cloudless-firstboot cloudless-updater; do
      version="$(dpkg-query -W -f='${Version}' "$package")"
      [[ "$version" == "$expected" ]] || {
        echo "$package version is $version; expected $expected" >&2
        return 1
      }
    done
  }

  install_generation "$baseline" "$expected_old"
  install -d -m 0750 /var/lib/cloudless
  qualification_engine=vllm
  if [ "$(dpkg --print-architecture)" = arm64 ]; then
    qualification_engine=llamacpp
  fi
  export CLOUDLESS_QUALIFICATION_ENGINE="$qualification_engine"
  printf '{"engine":"%s","model":"qualification/model","engineUnloaded":false,"executionMode":"local"}\n' \
    "$qualification_engine" >/var/lib/cloudless/state.json
  chown cloudlessd:cloudless-control /var/lib/cloudless/state.json
  chmod 0640 /var/lib/cloudless/state.json
  printf '{"qualification":"preserve-me"}\n' >/var/lib/cloudless/qualification-preserve.json
  legacy_cache=/var/lib/docker/volumes/cloudless-hf/_data
  qualification_revision="$(printf '3%.0s' $(seq 1 40))"
  qualification_repo="$legacy_cache/hub/models--qualification--model"
  install -d -m 0755 \
    "$qualification_repo/blobs" \
    "$qualification_repo/refs" \
    "$qualification_repo/snapshots/$qualification_revision"
  printf 'preserve-model-weights\n' >"$legacy_cache/hub/models--qualification--model/blobs/weights"
  ln -s ../../blobs/weights "$qualification_repo/snapshots/$qualification_revision/weights"
  printf '%s\n' "$qualification_revision" >"$qualification_repo/refs/main"
  getent passwd cloudlessd >/dev/null
  getent group cloudless-control >/dev/null
  getent group cloudless >/dev/null
  id -nG cloudlessd | tr ' ' '\n' | grep -Fxq cloudless-control
  id -nG cloudlessd | tr ' ' '\n' | grep -Fxq cloudless
  if id -nG cloudless | tr ' ' '\n' | grep -Fxq cloudless-control; then
    echo "desktop account unexpectedly has control-broker authority" >&2
    exit 1
  fi
  /usr/lib/cloudless/cloudless-engine \
    --prepare-model-cache-only \
    --legacy-model-cache-source="$legacy_cache"
  migrated=/var/lib/cloudless/models-cache/hub/models--qualification--model/blobs/weights
  cmp "$legacy_cache/hub/models--qualification--model/blobs/weights" "$migrated"
  test "$(stat -c '%U:%G:%a' /var/lib/cloudless/models-cache)" = "cloudlessd:cloudless:2750"
  test "$(stat -c '%G' "$(dirname "$migrated")")" = cloudless
  migrated_dir_mode="$(stat -c '%a' "$(dirname "$migrated")")"
  (( (8#$migrated_dir_mode & 8#2000) != 0 ))
  # A second invocation proves the package-owned migration marker is restart-safe.
  /usr/lib/cloudless/cloudless-engine \
    --prepare-model-cache-only \
    --legacy-model-cache-source="$legacy_cache"

  cat >/tmp/cloudless-fake-engine-broker.py <<'PY'
import grp
import json
import os
import socket
import threading
import time

path = "/run/cloudless/engine.sock"
os.makedirs(os.path.dirname(path), exist_ok=True)
try:
    os.unlink(path)
except FileNotFoundError:
    pass
server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
server.bind(path)
os.chown(path, 0, grp.getgrnam("cloudless-control").gr_gid)
os.chmod(path, 0o660)
server.listen(16)
def handle(connection):
    try:
        request = json.loads(connection.makefile("rb").readline())
        if request.get("action") == "image.remote-manifest" and os.path.exists("/tmp/cloudless-hold-recipe-check"):
            while os.path.exists("/tmp/cloudless-hold-recipe-check"):
                time.sleep(0.05)
        response = {"done": True}
        if request.get("action") == "container.list":
            response["containers"] = [
                {
                    "id": "qualification-vllm",
                    "name": "cloudless-vllm",
                    "image": "qualification/vllm@sha256:" + "1" * 64,
                    "state": "running",
                    "status": "Up for package qualification",
                    "ports": "127.0.0.1:8000->8000/tcp",
                },
                {
                    "id": "qualification-llamacpp",
                    "name": "cloudless-llamacpp",
                    "image": "qualification/llamacpp@sha256:" + "1" * 64,
                    "state": "running",
                    "status": "Up for package qualification",
                    "ports": "127.0.0.1:8000->8000/tcp",
                },
            ]
        elif request.get("action") in {"volume.list", "container.by-label", "container.by-ancestor"}:
            response["strings"] = []
        connection.sendall((json.dumps(response) + "\n").encode())
    finally:
        connection.close()

while True:
    connection, _ = server.accept()
    threading.Thread(target=handle, args=(connection,), daemon=True).start()
PY
  cat >/tmp/cloudless-fake-model.py <<'PY'
from hashlib import sha256
from http.server import BaseHTTPRequestHandler, HTTPServer
import json

CACHE = "/var/lib/cloudless/models-cache/hub/models--qualification--model/blobs/weights"

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/v1/models":
            self.send_error(404)
            return
        with open(CACHE, "rb") as model:
            digest = sha256(model.read()).hexdigest()
        body = json.dumps({"data": [{"id": "cloudless"}], "cacheSha256": digest}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *_):
        pass

HTTPServer(("127.0.0.1", 8000), Handler).serve_forever()
PY
  install -d -o root -g cloudless-control -m 0750 /run/cloudless
  python3 /tmp/cloudless-fake-engine-broker.py >/tmp/cloudless-fake-engine.log 2>&1 &
  fake_engine_pid=$!
  runuser -u cloudlessd -- python3 /tmp/cloudless-fake-model.py >/tmp/cloudless-fake-model.log 2>&1 &
  fake_model_pid=$!
  # This scenario models a daemon-only package restart: inference is already
  # serving before cloudlessd is replaced. Waiting here keeps it distinct from
  # the cold-boot recovery scenario, where the endpoint is intentionally absent.
  python3 <<'PY'
import time
import urllib.request

deadline = time.time() + 10
while time.time() < deadline:
    try:
        with urllib.request.urlopen("http://127.0.0.1:8000/v1/models", timeout=1) as response:
            if response.status == 200:
                raise SystemExit(0)
    except Exception:
        time.sleep(0.1)
raise SystemExit("fake inference endpoint did not become ready")
PY
  cloudlessd_pid=
  cleanup_continuity() {
    for pid in "$cloudlessd_pid" "$fake_model_pid" "$fake_engine_pid"; do
      test -n "$pid" || continue
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    done
  }
  trap cleanup_continuity EXIT

  start_continuity_daemon() {
    runuser -u cloudlessd -- env \
      CLOUDLESS_ADDR=127.0.0.1:18775 \
      CLOUDLESS_GATEWAY_ADDR=127.0.0.1:18776 \
      CLOUDLESS_STATE_DIR=/var/lib/cloudless \
      CLOUDLESS_ENGINE_SOCKET=/run/cloudless/engine.sock \
      CLOUDLESS_NO_PROVISION=1 \
      /bin/sh -c 'umask 0077; exec /usr/lib/cloudless/cloudlessd' \
      >/tmp/cloudless-continuity-daemon.log 2>&1 &
    cloudlessd_pid=$!
  }
  stop_continuity_daemon() {
    test -n "$cloudlessd_pid" || return 0
    kill "$cloudlessd_pid"
    wait "$cloudlessd_pid" || true
    cloudlessd_pid=
  }
  assert_active_model() {
    local expected_digest
    expected_digest="$(sha256sum "$migrated" | awk '{print $1}')"
    python3 - "$expected_digest" "$qualification_engine" <<'PY'
import json
import sys
import time
import urllib.request

expected, expected_engine = sys.argv[1:]
deadline = time.time() + 10
last = ""
while time.time() < deadline:
    try:
        with urllib.request.urlopen("http://127.0.0.1:18775/api/engine", timeout=2) as response:
            engine = json.load(response)
        with urllib.request.urlopen("http://127.0.0.1:8000/v1/models", timeout=2) as response:
            model = json.load(response)
        if (
            engine.get("active") == expected_engine
            and engine.get("ready") is True
            and engine.get("unloaded") is False
            and model.get("data") == [{"id": "cloudless"}]
            and model.get("cacheSha256") == expected
        ):
            raise SystemExit(0)
        last = repr((engine, model))
    except Exception as exc:
        last = repr(exc)
    time.sleep(0.1)
raise SystemExit("active model continuity failed: " + last)
PY
  }

  begin_interrupted_recipe_check() {
    touch /tmp/cloudless-hold-recipe-check
    python3 <<'PY' > /tmp/cloudless-recipe-operation-id
import json
import urllib.request

digest = "2" * 64
revision = "3" * 40
engine_type = "vllm"
draft = {
    "name": "Package continuity recipe",
    "description": "A constrained recipe used to qualify durable preparation across package transitions.",
    "platform": "generic",
    "source": {"url": "", "revision": "", "files": {}},
    "engine": {
        "type": engine_type, "image": f"qualification/{engine_type}@sha256:{digest}",
        "servedModelName": "cloudless", "containerPort": 8890, "apiPath": "/v1",
        "proxyHost": "host.docker.internal", "restartPolicy": "no", "arguments": [],
    },
    "model": {
        "id": "qualification/model", "revision": revision, "quantization": "none",
        "dtype": "auto", "kvCacheDtype": "auto", "maxContext": 32768, "maxSequences": 1,
        "gpuMemoryUtilization": 0.5, "tensorParallel": 1, "pipelineParallel": 1,
        "trustRemoteCode": False,
    },
    "distributed": {
        "nodes": 1, "backend": "nccl", "masterPort": 25000, "interface": "",
        "hca": "", "ibGidIndex": 0, "workerAlias": "cloudless-recipe-worker",
        "selectedNodes": [],
    },
    "runtime": {
        "adapter": "managed-container-v1", "workingDir": ".", "timeoutMinutes": 480,
        "prerequisites": [], "environment": {"HF_HUB_DISABLE_XET": "1"},
        "lifecycle": {
            "build": {"program": "", "args": []}, "download": {"program": "", "args": []},
            "start": {"program": "", "args": []}, "stop": {"program": "", "args": []},
        },
    },
    "health": {
        "scheme": "http", "host": "127.0.0.1", "port": 8890, "path": "/health",
        "timeoutSeconds": 7200, "intervalSeconds": 3,
    },
}

def request(path, payload):
    data = json.dumps(payload).encode()
    req = urllib.request.Request("http://127.0.0.1:18775" + path, data=data,
                                 headers={"Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(req, timeout=5) as response:
        return json.load(response)

recipe = request("/api/recipes", draft)
operation = request(f"/api/recipes/{recipe['id']}/check", {})
print(operation["operationId"])
PY
    recipe_operation_id="$(tr -d '[:space:]' < /tmp/cloudless-recipe-operation-id)"
    test -n "$recipe_operation_id"
    assert_durable_recipe_check "$recipe_operation_id"
  }

  assert_durable_recipe_check() {
    python3 - "$1" <<'PY'
import json
import sys
import time
import urllib.request

operation_id = sys.argv[1]
deadline = time.time() + 10
last = ""
while time.time() < deadline:
    try:
        with urllib.request.urlopen("http://127.0.0.1:18775/api/recipes", timeout=2) as response:
            payload = json.load(response)
        operation = next((item for item in payload.get("operations", []) if item.get("id") == operation_id), None)
        if operation and operation.get("phase") in {"checking", "recovering"}:
            raise SystemExit(0)
        last = repr(operation)
    except Exception as exc:
        last = repr(exc)
    time.sleep(0.1)
raise SystemExit("durable recipe preparation was not preserved: " + last)
PY
  }

  start_continuity_daemon
  assert_active_model
  begin_interrupted_recipe_check

  # Simulate control-plane data created by the former root daemon. The
  # candidate postinst must migrate it without deleting the enrolled cluster.
  install -d -o root -g root -m 0700 /var/lib/cloudless/cluster
  printf '{"role":"coordinator","peerHost":"192.0.2.10"}\n' >/var/lib/cloudless/cluster/state.json
  printf 'legacy-private-key\n' >/var/lib/cloudless/cluster/id_ed25519
  chown root:root /var/lib/cloudless/cluster/state.json /var/lib/cloudless/cluster/id_ed25519
  chmod 0600 /var/lib/cloudless/cluster/state.json /var/lib/cloudless/cluster/id_ed25519

  install -d -o root -g root -m 0700 /var/lib/cloudless/secrets
  printf 'legacy-hermes-key\n' >/var/lib/cloudless/secrets/hermes-api-key
  chown root:root /var/lib/cloudless/secrets/hermes-api-key
  chmod 0600 /var/lib/cloudless/secrets/hermes-api-key

  # Upgrade while the old daemon and its model endpoint are live, then restart
  # into the candidate binary and prove the exact cache-backed runtime again.
  install_generation "$candidate" "$expected_new"
  test "$(stat -c '%U:%G:%a' /var/lib/cloudless/cluster)" = "cloudlessd:cloudless-control:700"
  test "$(stat -c '%U:%G:%a' /var/lib/cloudless/cluster/state.json)" = "cloudlessd:cloudless-control:600"
  test "$(stat -c '%U:%G:%a' /var/lib/cloudless/cluster/id_ed25519)" = "cloudlessd:cloudless-control:600"
  runuser -u cloudlessd -- grep -Fq '192.0.2.10' /var/lib/cloudless/cluster/state.json
  test "$(stat -c '%U:%G:%a' /var/lib/cloudless/secrets)" = "cloudlessd:cloudless-control:700"
  test "$(stat -c '%U:%G:%a' /var/lib/cloudless/secrets/hermes-api-key)" = "cloudlessd:cloudless-control:600"
  runuser -u cloudlessd -- grep -Fqx 'legacy-hermes-key' /var/lib/cloudless/secrets/hermes-api-key
  grep -Fq '"qualification":"preserve-me"' /var/lib/cloudless/qualification-preserve.json
  cmp "$legacy_cache/hub/models--qualification--model/blobs/weights" "$migrated"
  assert_active_model
  assert_durable_recipe_check "$recipe_operation_id"
  stop_continuity_daemon
  start_continuity_daemon
  assert_active_model
  assert_durable_recipe_check "$recipe_operation_id"

  desktop_socket=/run/cloudless-desktop/qualification.sock
  install -d -o root -g cloudless -m 0770 /run/cloudless-desktop
  runuser -u cloudless -- \
    /usr/bin/cloudless-desktop-agent --socket "$desktop_socket" >/tmp/cloudless-desktop-agent.log 2>&1 &
  desktop_agent_pid=$!
  for _ in $(seq 1 50); do
    test -S "$desktop_socket" && break
    sleep 0.1
  done
  test -S "$desktop_socket" || {
    cat /tmp/cloudless-desktop-agent.log >&2
    echo "desktop-session agent did not create its socket" >&2
    exit 1
  }
  test "$(stat -c '%U:%G:%a' "$desktop_socket")" = "cloudless:cloudless:660"
  cat >/tmp/cloudless-desktop-query.py <<'PY'
import json, socket, sys
client = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
client.connect(sys.argv[1])
client.sendall(b'{"action":"display.query"}\n')
print(json.loads(client.makefile("rb").readline()))
PY
  root_response="$(python3 - "$desktop_socket" <<'PY'
import json, socket, sys
client = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
client.connect(sys.argv[1])
print(json.loads(client.makefile("rb").readline()))
PY
)"
  printf '%s\n' "$root_response" | grep -Fq 'desktop client is not authorized'
  daemon_response="$(runuser -u cloudlessd -- python3 /tmp/cloudless-desktop-query.py "$desktop_socket")"
  if printf '%s\n' "$daemon_response" | grep -Fq 'desktop client is not authorized'; then
    echo "desktop-session agent rejected the exact cloudlessd identity" >&2
    exit 1
  fi
  printf '%s\n' "$daemon_response" | grep -Fq 'query display'
  kill "$desktop_agent_pid"
  wait "$desktop_agent_pid" || true

  CLOUDLESS_DISPOSABLE_PACKAGE_TEST=1 /qualification/session-test.sh

  # Roll back while the candidate daemon still serves the migrated model, then
  # restart into the baseline generation without losing endpoint or cache.
  install_generation "$baseline" "$expected_old"
  grep -Fq '"qualification":"preserve-me"' /var/lib/cloudless/qualification-preserve.json
  cmp "$legacy_cache/hub/models--qualification--model/blobs/weights" "$migrated"
  assert_active_model
  assert_durable_recipe_check "$recipe_operation_id"
  stop_continuity_daemon
  start_continuity_daemon
  assert_active_model
  assert_durable_recipe_check "$recipe_operation_id"
  rm -f /tmp/cloudless-hold-recipe-check
  stop_continuity_daemon
  cleanup_continuity
  trap - EXIT
  test -x /usr/lib/cloudless/cloudlessd
  test -x /usr/lib/cloudless/cloudless-engine
  test -x /usr/lib/cloudless/cloudless-model-storage
  test -x /usr/bin/cloudless-desktop-agent
  test -x /usr/bin/cloudless-qualify
  test -s /usr/share/cloudless/physical-validation-matrix.json
  test -x /usr/sbin/cloudless-backup
  test -x /usr/bin/cloudless-kiosk
  test -x /usr/bin/cloudless-display-watch
  echo "Debian install, upgrade and rollback lifecycle passed."
  exit 0
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PACKAGES="${CLOUDLESS_PACKAGE_OUT:-$ROOT/distro/out/packages}"
VERSION="${CLOUDLESS_VERSION:-$(tr -d '[:space:]' < "$ROOT/distro/VERSION")}"
ARCH="${CLOUDLESS_TEST_ARCH:-amd64}"
case "$ARCH" in amd64|arm64) ;; *) echo "Unsupported lifecycle architecture: $ARCH" >&2; exit 2 ;; esac
for command in docker dpkg-deb; do
  command -v "$command" >/dev/null || { echo "Missing package-lifecycle command: $command" >&2; exit 1; }
done

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/baseline" "$work/candidate"
baseline_version="${VERSION}~qualification1"
for package in cloudless-orchestrator cloudless-shell cloudless-branding cloudless-hardware cloudless-firstboot cloudless-updater; do
  source="$PACKAGES/${package}_${VERSION}_${ARCH}.deb"
  [[ -s "$source" ]] || { echo "Missing lifecycle candidate: $source" >&2; exit 1; }
  cp "$source" "$work/candidate/"
  root="$work/root-$package"
  dpkg-deb -R "$source" "$root"
  sed -i "s/^Version:.*/Version: $baseline_version/" "$root/DEBIAN/control"
  dpkg-deb --root-owner-group --build "$root" "$work/baseline/${package}_${baseline_version}_${ARCH}.deb" >/dev/null
done

platform="linux/$ARCH"
docker run --rm --platform "$platform" \
  -e CLOUDLESS_TRACE \
  -v "$work/baseline:/qualification/baseline:ro" \
  -v "$work/candidate:/qualification/candidate:ro" \
  -v "$ROOT/distro/scripts/test-package-lifecycle.sh:/qualification/test.sh:ro" \
  -v "$ROOT/distro/scripts/test-installed-session.sh:/qualification/session-test.sh:ro" \
  ubuntu:24.04 bash /qualification/test.sh --inside \
    /qualification/baseline /qualification/candidate "$baseline_version" "$VERSION"
