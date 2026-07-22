# Hermes integration

CloudlessOS integrates the official Nous Research Hermes Agent container rather
than embedding Hermes Desktop or maintaining a downstream Hermes image.

## Current foundation

- Hermes Agent is pinned to the stable `v2026.7.20` multi-architecture image
  digest rather than a moving tag.
- Its browser dashboard opens locally at `http://127.0.0.1:9119`.
- Hermes uses the active Cloudless OpenAI-compatible engine at
  `http://127.0.0.1:8000/v1` with the stable model name `cloudless`.
- Configuration, sessions, skills and memory persist in Cloudless state and are
  mounted at the official container path, `/opt/data`.
- The container remains unprivileged. Its state directory is owned by Hermes'
  documented container UID (10000).
- Telegram and Discord credentials remain optional and editable through the
  existing Cloudless app settings.

The dashboard binds only to host loopback. That lets the local Cloudless shell
open it without publishing Hermes' configuration and secrets on the LAN. The
existing Cloudless sharing controls must not expose this app until authenticated
remote dashboard access has been designed and tested.

## Validation gate

Before this branch is merged for release:

1. Pull and launch Hermes from a CloudlessOS test appliance.
2. Confirm `http://127.0.0.1:9119/api/status` responds.
3. Send a dashboard message and confirm it reaches the active Cloudless model.
4. Restart the container and verify sessions and memory persist.
5. Save configuration in both Cloudless settings and the Hermes dashboard.
6. Validate Telegram and Discord independently with allow-all disabled.
7. Confirm the pinned multi-architecture image digest pulls on amd64 hardware.

## Next integration slices

- Add a Cloudless-native Hermes onboarding panel and readiness indicator.
- Surface Hermes health and version through the orchestrator API.
- Add explicit permissions for workspace, terminal and browser tools.
- Provide opt-in authenticated LAN access without exposing secrets by default.
- Evaluate Hermes Desktop only as an optional remote client, not as a nested
  Electron application inside the Cloudless web shell.
