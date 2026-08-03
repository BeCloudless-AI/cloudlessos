#!/usr/bin/env bash
# Prevent already-delegated host authority from leaking back into the HTTP
# daemon. This is deliberately narrower than the unfinished Docker/network
# split and grows as each authority domain is migrated.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ORCHESTRATOR="$ROOT/orchestrator"

mapfile -d '' delegated_sources < <(
  find \
    "$ORCHESTRATOR/internal/api" \
    "$ORCHESTRATOR/internal/locale" \
    "$ORCHESTRATOR/internal/remoteaccess" \
    -type f -name '*.go' ! -name '*_test.go' -print0
)

if grep -En 'exec\.Command(Context)?\([^)]*"(/usr/bin/)?(systemctl|timedatectl)"|Run\([^)]*"systemctl"' \
  "${delegated_sources[@]}"; then
  echo "Delegated host actions must cross cloudless-privileged, not execute in the HTTP daemon." >&2
  exit 1
fi

protocol="$ORCHESTRATOR/internal/privileged/protocol.go"
broker="$ORCHESTRATOR/cmd/cloudless-privileged/main.go"
service="$ROOT/distro/packages/cloudless-orchestrator/cloudless-privileged.service"
postinst="$ROOT/distro/packages/cloudless-orchestrator/postinst"
build="$ROOT/distro/scripts/build-packages.sh"

for required in \
  ActionPowerOff \
  ActionReboot \
  ActionSystemUpdateCheck \
  ActionSystemUpdateApply \
  ActionNVIDIAUpdateCheck \
  ActionNVIDIAUpdateApply \
  ActionTailscaleInstaller \
  ActionTimezoneSet \
  ActionClusterNetworkApply \
  ActionClusterNetworkRemove \
  ActionModelUninstall \
  ActionModelViewsReconcile
do
  grep -Fq "$required" "$protocol"
  grep -Fq "$required" "$broker"
done

grep -Fq 'decoder.DisallowUnknownFields()' "$protocol"
grep -Fq 'time.LoadLocation(req.Value)' "$protocol"
grep -Fq 'ValidModelID(req.Value)' "$protocol"
grep -Fq 'syscall.SO_PEERCRED' "$ORCHESTRATOR/internal/privileged/peer_linux.go"
grep -Fqx 'User=root' "$service"
grep -Fqx 'ProtectSystem=strict' "$service"
grep -Fq 'cloudless-control' "$postinst"
grep -Fq 'adduser --system --no-create-home' "$postinst"
grep -Fq '"$model_view"/*/.cloudless-revision' "$postinst"
grep -Fq 'cloudless-privileged.service' "$build"
grep -Fq 'cloudless-privileged"' "$build"
grep -Fq 'cloudless-engine.service' "$build"
grep -Fq 'cloudless-engine"' "$build"
grep -Fq 'cloudless-docker"' "$build"
grep -Fq 'engine.ServeBroker' "$ORCHESTRATOR/cmd/cloudless-engine/main.go"
grep -Fq 'privileged.AuthorizePeer' "$ORCHESTRATOR/cmd/cloudless-engine/main.go"
grep -Fq 'ReviewedRecipePolicy' "$ORCHESTRATOR/cmd/cloudless-engine/main.go"
grep -Fq 'recipeDockerCompatibilityExecutable = "/usr/lib/cloudless/cloudless-docker"' \
  "$ORCHESTRATOR/internal/api/local_recipes.go"
if grep -R -Fq '/usr/bin/docker' "$ORCHESTRATOR/internal/api"; then
  echo "The unprivileged API service must not invoke the Docker CLI directly." >&2
  exit 1
fi
if grep -REn '\.VolumeMountpoint\(' "$ORCHESTRATOR/internal/api" --include='*.go' --exclude='*_test.go'; then
  echo "The API must not inspect root-private Docker volume mountpoints." >&2
  exit 1
fi
if grep -R -Fq 'nsenter' "$ORCHESTRATOR/internal/api" --include='*.go' --exclude='*_test.go'; then
  echo "The API must not enter the host mount namespace." >&2
  exit 1
fi
grep -Fq 'engine.NewBrokerClient' "$ORCHESTRATOR/cmd/cloudlessd/main.go"
grep -Fqx 'User=cloudlessd' "$ROOT/distro/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fqx 'Group=cloudless-control' "$ROOT/distro/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fqx 'SupplementaryGroups=cloudless' "$ROOT/distro/packages/cloudless-orchestrator/cloudlessd.service"
if grep -Fq '/run/docker.sock' "$ROOT/distro/packages/cloudless-orchestrator/cloudlessd.service"; then
  echo "cloudlessd must not have Docker socket access." >&2
  exit 1
fi
grep -Fq 'ReadWritePaths=/etc/netplan' "$service"
if grep -Fq '/etc/netplan' "$ROOT/distro/packages/cloudless-orchestrator/cloudlessd.service"; then
  echo "cloudlessd must not retain Netplan write authority after delegation." >&2
  exit 1
fi

cluster="$ORCHESTRATOR/internal/sparkcluster/cluster.go"
if grep -En 'run\([^)]*"netplan"|os\.(WriteFile|Remove)\([^)]*configPath' "$cluster"; then
  echo "Local Spark networking must cross cloudless-privileged." >&2
  exit 1
fi
grep -Fq 'ConfigureClusterNetwork(ctx, links, nodeIndex)' "$cluster"
grep -Fq 'ActionClusterNetworkRemove' "$cluster"
grep -Fq 'return "sshpass", append([]string{"-d", "3", "ssh"}' "$cluster"
grep -Fq 'cmd.ExtraFiles = []*os.File{reader}' "$cluster"
if grep -Fq 'SSHPASS=' "$cluster"; then
  echo "Spark administrator passwords must not be exposed through the process environment." >&2
  exit 1
fi

model_cache="$ORCHESTRATOR/internal/modelcache/migration.go"
engine_service="$ROOT/distro/packages/cloudless-orchestrator/cloudless-engine.service"
privileged_service="$ROOT/distro/packages/cloudless-orchestrator/cloudless-privileged.service"
grep -Fqx 'Group=cloudless-control' "$engine_service"
grep -Fqx 'RuntimeDirectoryMode=0750' "$engine_service"
grep -Fqx 'Group=cloudless-control' "$privileged_service"
grep -Fqx 'RuntimeDirectoryMode=0750' "$privileged_service"
daemon_service="$ROOT/distro/packages/cloudless-orchestrator/cloudlessd.service"
grep -Eq 'DefaultRoot[[:space:]]*=[[:space:]]*"/var/lib/cloudless/models-cache"' "$model_cache"
grep -Fq 'modelcache.Prepare(root, source, uid, gid)' "$ORCHESTRATOR/cmd/cloudless-engine/main.go"
grep -Fq 'CLOUDLESS_MODEL_CACHE=/var/lib/cloudless/models-cache' "$engine_service"
grep -Fq 'CLOUDLESS_MODEL_CACHE=/var/lib/cloudless/models-cache' "$daemon_service"
grep -Fq 'ReadWritePaths=/run/cloudless /var/lib/cloudless' "$engine_service"
grep -Fq '/var/lib/cloudless/models-cache:/root/.cache/huggingface' "$cluster"
grep -Fq 'install -d -m 2750 -o cloudlessd -g cloudless /var/lib/cloudless/models-cache' "$postinst"
if grep -Eq 'usermod .*cloudless-control.* cloudless([[:space:]]|$)' "$postinst"; then
  echo "The desktop account must never receive control-broker authority." >&2
  exit 1
fi

desktop_protocol="$ORCHESTRATOR/internal/desktop/protocol.go"
desktop_agent="$ORCHESTRATOR/cmd/cloudless-desktop-agent/main.go"
grep -Fq 'ActionDisplayQuery Action = "display.query"' "$desktop_protocol"
grep -Fq 'ActionDisplayApply Action = "display.apply"' "$desktop_protocol"
grep -Fq 'ActionInputKey     Action = "input.key"' "$desktop_protocol"
grep -Fq 'privileged.AuthorizePeerUID' "$desktop_agent"
grep -Fq '/usr/bin/cloudless-desktop-agent &' "$ROOT/distro/packages/cloudless-shell/openbox-autostart"
grep -Fq '2775 /home/cloudless/Cloudless' "$ROOT/distro/packages/cloudless-shell/postinst"
grep -Fq 'tailscale set --operator=cloudlessd' \
  "$ROOT/distro/packages/cloudless-orchestrator/cloudless-install-tailscale"
if grep -REn 'exec\.Command(Context)?\([^)]*"(xrandr|xdotool)"|XAUTHORITY=' \
  "$ORCHESTRATOR/internal/api" --include='*.go' --exclude='*_test.go'; then
  echo "X11 authority must remain in the cloudless desktop-session agent." >&2
  exit 1
fi

engine_policy="$ORCHESTRATOR/internal/engine/policy.go"
docker_engine="$ORCHESTRATOR/internal/engine/docker.go"
grep -Fq 'func ValidateRunSpec' "$engine_policy"
grep -Fq 'mounting the Docker socket is forbidden' "$engine_policy"
grep -Fq 'outside Cloudless-owned storage' "$engine_policy"
grep -Fq 'container policy:' "$docker_engine"
grep -Fq 'ValidateRunSpec(spec)' "$docker_engine"
grep -Fq 'RunTransient(ctx context.Context, spec RunSpec)' "$ORCHESTRATOR/internal/engine/engine.go"
if grep -REn '\.Output\([^)]*"run"' \
  "$ORCHESTRATOR/internal/api" \
  "$ORCHESTRATOR/internal/provision" \
  --include='*.go' --exclude='*_test.go'; then
  echo "Transient containers must use policy-validated RunTransient, not raw docker run arguments." >&2
  exit 1
fi
if grep -REn '(s\.eng|i\.server\.eng|[^[:alnum:]_]eng)\.Output\(' \
  "$ORCHESTRATOR/internal/api" \
  "$ORCHESTRATOR/internal/apps" \
  "$ORCHESTRATOR/internal/provision" \
  --include='*.go' --exclude='*_test.go'; then
  echo "Container operations must use typed Engine methods; the generic Output channel is forbidden." >&2
  exit 1
fi

for source in \
  "$ORCHESTRATOR/internal/api/models.go" \
  "$ORCHESTRATOR/internal/api/local_recipes.go" \
  "$ORCHESTRATOR/internal/api/recipe_managed_container.go" \
  "$ORCHESTRATOR/internal/provision/promotion.go"
do
  grep -Fq 'HF_TOKEN_PATH' "$source"
  if grep -Fq 'env["HF_TOKEN"]' "$source"; then
    echo "Managed model containers must mount the protected Hugging Face token file, not persist its value in Docker environment metadata." >&2
    exit 1
  fi
done
grep -Fq 'SecretFiles  map[string]string' "$ORCHESTRATOR/internal/engine/engine.go"
grep -Fq 'container secret must be owner-only' "$ORCHESTRATOR/internal/engine/policy.go"
grep -Fq 'admittedReviewedSecretMount' "$ORCHESTRATOR/internal/engine/reviewed_recipe_policy.go"
grep -Fq 'parts[2] != "ro"' "$ORCHESTRATOR/internal/engine/reviewed_recipe_policy.go"

echo "Delegated privilege boundaries are enforced."
