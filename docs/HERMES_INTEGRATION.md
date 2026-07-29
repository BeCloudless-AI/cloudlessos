# Hermes Agent integration

CloudlessOS uses Nous Research Hermes Agent as its built-in agentic runtime. Hermes is a core
service, not Hermes Desktop and not a Cloudless-maintained fork.

## Runtime contract

- The official multi-architecture Hermes Agent image is pinned by registry digest in the app
  manifest.
- Hermes follows the currently active Cloudless inference engine through the stable local OpenAI-
  compatible endpoint.
- The Cloudless Assistant sends conversations to Hermes and renders response/tool progress in the
  desktop interface.
- The full Hermes dashboard is available through the Cloudless embedded viewer and remains local by
  default.
- Configuration, sessions, skills, memory and workspace data persist in Cloudless-owned app state.
- Resetting Hermes restores its Cloudless model connection; Hermes cannot be uninstalled as an
  ordinary optional app.

The Assistant label says “Powered by Hermes Agent,” but Cloudless owns model selection, fit checks,
downloads and engine lifecycle. Hermes must use Cloudless model APIs instead of inventing an
independent model installer.

## API access and permissions

The shareable Cloudless gateway on port `8766` exposes two authenticated surfaces:

- `/v1/*` for OpenAI-compatible model inference;
- `/agent/v1/*` and `/agent/api/*` for Hermes Agent APIs.

User-created keys have `model`, `agent` or `both` scope. A key with the wrong scope receives `403`;
a missing or invalid key receives `401`. Cloudless removes the user credential before proxying and
uses a separate random machine-local Hermes credential that is never returned to the browser.
Per-key metrics distinguish model and agent requests.

LAN access and opt-in public tunnels expose the same scoped gateway. They do not directly expose
the internal Hermes credential. Agent keys are high privilege because Hermes can use configured
local tools.

## Release validation

For every pinned Hermes image or integration change:

1. Verify the image exists for every advertised architecture.
2. Start from clean Cloudless state and confirm Hermes provisions successfully.
3. Verify the internal health endpoint and embedded dashboard.
4. Send an Assistant message and confirm stable streamed text and tool progress.
5. Restart and verify credentials, sessions and memory persist.
6. Test model-only, agent-only and combined keys over loopback and LAN.
7. If public sharing changed, test a temporary tunnel and confirm internal-only surfaces stay private.
8. Verify Telegram and Discord independently when those connectors are enabled.

Hermes Desktop can remain an optional remote client. It is not required for CloudlessOS.
