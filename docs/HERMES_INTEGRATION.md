# Hermes integration

CloudlessOS uses the official Nous Research Hermes Agent as its built-in
agentic runtime. Hermes is a preinstalled core service, not a nested Hermes
Desktop application and not a Cloudless-maintained fork.

## Architecture

- The official multi-architecture `nousresearch/hermes-agent:v2026.7.20`
  image is pinned by registry digest.
- Hermes starts during Cloudless provisioning and uses the active local model
  through `http://127.0.0.1:8000/v1`.
- The built-in Cloudless Assistant sends its conversations to Hermes' API and
  renders Hermes tool progress alongside response text.
- The complete Hermes dashboard is available locally at
  `http://127.0.0.1:9119`. It binds to loopback and is never passed through the
  LAN or public gateway.
- Configuration, sessions, skills, memory and the agent workspace persist in
  Cloudless state under the single `/opt/data` container mount.
- Telegram and Discord credentials remain optional and editable through the
  existing Cloudless app settings.

## API access and permissions

The shareable Cloudless gateway on port 8766 exposes two authenticated
surfaces:

- `/v1/*` is raw OpenAI-compatible model inference.
- `/agent/v1/*` and `/agent/api/*` are the Hermes agent APIs.

Every user-created key has a `model`, `agent`, or `both` scope. Legacy keys are
model-only. A key with the wrong scope receives HTTP 403; a missing or invalid
key receives HTTP 401. Cloudless removes the user key before proxying and uses
a separate random, machine-local Hermes credential that is generated on first
boot, stored with the app state, and never returned to the browser. Per-key
metrics track model and agent requests separately.

LAN access and opt-in public Cloudflare tunnels expose the same scoped gateway.
They do not expose the Hermes dashboard or its internal credential. Agent keys
should be treated as high privilege because Hermes can use its configured local
tools.

## Appliance validation gate

Before merging this branch into a release:

1. Boot a clean CloudlessOS appliance and confirm Hermes is preinstalled.
2. Confirm `http://127.0.0.1:8642/health` and the local dashboard on port 9119
   respond.
3. Send a built-in Assistant message and verify text and tool progress stream.
4. Restart the machine and verify the internal credential, sessions and memory
   persist.
5. Create model-only, agent-only and combined keys; verify forbidden paths
   return 403 and allowed paths work over loopback and LAN.
6. Enable a temporary public tunnel and verify agent access while confirming
   the dashboard cannot be reached through it.
7. Validate Telegram and Discord independently with allow-all disabled.
8. Pull the pinned image on each supported architecture before release.

Hermes Desktop remains a possible optional remote client. It is not required by
CloudlessOS and should not be embedded into the browser shell.
