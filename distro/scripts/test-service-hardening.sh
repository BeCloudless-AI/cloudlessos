#!/usr/bin/env bash
# Enforce the minimum sandbox contract for package-owned host services. Some
# units remain root because they install packages, manage Docker or repair the
# display; this gate still prevents ambient privilege escalation and permissive
# file creation from becoming their defaults.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

units=(
  distro/packages/cloudless-firstboot/cloudless-firstboot.service
  distro/packages/cloudless-firstboot/cloudless-graphical-recovery.service
  distro/packages/cloudless-hardware/cloudless-hardware.service
  distro/packages/cloudless-orchestrator/cloudless-tailscale-install.service
  distro/packages/cloudless-orchestrator/cloudless-privileged.service
  distro/packages/cloudless-orchestrator/cloudless-model-storage.service
  distro/packages/cloudless-orchestrator/cloudless-engine.service
  distro/packages/cloudless-orchestrator/cloudlessd.service
  distro/packages/cloudless-updater/cloudless-nvidia-apply.service
  distro/packages/cloudless-updater/cloudless-nvidia-check.service
  distro/packages/cloudless-updater/cloudless-update-apply.service
  distro/packages/cloudless-updater/cloudless-update-check.service
)

for relative in "${units[@]}"; do
  unit="$ROOT/$relative"
  test -s "$unit"
  grep -Fqx 'NoNewPrivileges=true' "$unit" || {
    echo "$relative must prevent acquisition of new privileges" >&2
    exit 1
  }
  grep -Fqx 'UMask=0077' "$unit" || {
    echo "$relative must create private files by default" >&2
    exit 1
  }
done

broker="$ROOT/distro/packages/cloudless-orchestrator/cloudless-privileged.service"
grep -Fqx 'User=root' "$broker"
grep -Fqx 'ProtectSystem=strict' "$broker"
grep -Fqx 'ProtectHome=read-only' "$broker"
grep -Fq '/var/lib/cloudless/models-cache' "$broker"
grep -Fq -- '-/home/cloudless/Cloudless/Models' "$broker"
grep -Fqx 'PrivateDevices=yes' "$broker"
grep -Fqx 'ExecStart=/usr/lib/cloudless/cloudless-privileged' "$broker"

# This narrowly scoped root helper is intentionally not placed in a private
# mount namespace: its sole purpose is to make the validated NFS mount visible
# to cloudlessd, the engine broker and containers. It receives only the mount
# capability and fixed, typed arguments from a root-owned configuration file.
model_storage="$ROOT/distro/packages/cloudless-orchestrator/cloudless-model-storage.service"
grep -Fqx 'User=root' "$model_storage"
grep -Fqx 'CapabilityBoundingSet=CAP_SYS_ADMIN CAP_DAC_OVERRIDE' "$model_storage"
grep -Fqx 'AmbientCapabilities=CAP_SYS_ADMIN CAP_DAC_OVERRIDE' "$model_storage"
grep -Fqx 'ExecStart=/usr/lib/cloudless/cloudless-model-storage mount' "$model_storage"
grep -Fqx 'ExecStop=/usr/lib/cloudless/cloudless-model-storage unmount' "$model_storage"
grep -Fqx 'Wants=network-online.target nfs-server.service' "$model_storage"
grep -Fqx 'After=network-online.target nfs-server.service' "$model_storage"
if grep -Eq '^(PrivateMounts|ProtectSystem|ProtectHome|PrivateTmp)=' "$model_storage"; then
  echo "cloudless-model-storage must mount in the host namespace" >&2
  exit 1
fi

daemon="$ROOT/distro/packages/cloudless-orchestrator/cloudlessd.service"
grep -Fqx 'StateDirectoryMode=0751' "$daemon" || {
  echo "cloudlessd must leave traversal-only access to the read-only model cache view" >&2
  exit 1
}
grep -Fq 'install -d -m 0751 -o cloudlessd -g cloudless-control /var/lib/cloudless' \
  "$ROOT/distro/packages/cloudless-orchestrator/postinst"

engine_broker="$ROOT/distro/packages/cloudless-orchestrator/cloudless-engine.service"
privileged_broker="$ROOT/distro/packages/cloudless-orchestrator/cloudless-privileged.service"
grep -Fqx 'User=root' "$engine_broker"
grep -Fqx 'ProtectSystem=strict' "$engine_broker"
grep -Fqx 'ExecStart=/usr/lib/cloudless/cloudless-engine' "$engine_broker"
grep -Fqx 'Group=cloudless-control' "$engine_broker"
grep -Fqx 'RuntimeDirectoryMode=0750' "$engine_broker"
grep -Fqx 'Group=cloudless-control' "$privileged_broker"
grep -Fqx 'RuntimeDirectoryMode=0750' "$privileged_broker"
grep -Fq '/run/docker.sock' "$engine_broker"
if grep -Fq '/run/docker.sock' "$ROOT/distro/packages/cloudless-orchestrator/cloudlessd.service"; then
  echo "Only cloudless-engine may retain Docker socket access." >&2
  exit 1
fi

protocol="$ROOT/orchestrator/internal/privileged/protocol.go"
grep -Eq 'ActionPowerOff[[:space:]]+Action = "power.shutdown"' "$protocol"
grep -Eq 'ActionReboot[[:space:]]+Action = "power.restart"' "$protocol"
grep -Fq 'ActionTailscaleInstaller' "$protocol"
grep -Fq 'ActionSystemUpdateApply' "$protocol"
if grep -Eq 'Command|Arguments|Args[[:space:]]' "$protocol"; then
  echo "Privileged broker protocol must not accept commands or arguments" >&2
  exit 1
fi

# The terminal is the intentional exception: ttyd is loopback-only and invokes
# /bin/login, whose PAM/session path must be able to establish the selected OS
# account. It still needs a private umask, origin checks and real OS login.
terminal="$ROOT/distro/packages/cloudless-shell/cloudless-terminal.service"
grep -Fqx 'UMask=0077' "$terminal"
grep -Fq -- '--interface 127.0.0.1' "$terminal"
grep -Fq -- '--check-origin' "$terminal"
grep -Fq '/bin/login' "$terminal"

tailscale="$ROOT/distro/packages/cloudless-orchestrator/cloudless-install-tailscale"
grep -Fq 'KEY_FINGERPRINT=2596A99EAAB33821893C0A79458CA832957F5868' "$tailscale"
grep -Fq 'KEY_SUBKEY_FINGERPRINT=2F625B3A774B946822EDDBEEB1547A3DDAAF03C6' "$tailscale"
grep -Fq 'signed-by=' "$tailscale"
if grep -Eq 'tailscale\.com/install\.sh|curl[^|]*\|[[:space:]]*(ba)?sh' "$tailscale"; then
  echo "Tailscale root installation must never execute a remotely downloaded shell" >&2
  exit 1
fi

bash "$ROOT/distro/scripts/test-privilege-boundaries.sh"

echo "Package-owned host services satisfy the minimum hardening contract."
