# CloudlessOS privacy boundaries

CloudlessOS does not include a Cloudless telemetry or analytics uploader. Model inference, Hermes
Agent conversations, hardware metrics, operation history and the security audit stay on the
machine unless the user deliberately exposes a service or sends data through an installed
application.

Network access still occurs for explicit product functions:

- signed updates, manifests and release metadata come from `updates.becloudless.ai`;
- model search/download and optional account authentication contact Hugging Face;
- container images come from their configured registries;
- Tailscale installation/login and Tailscale Serve contact Tailscale;
- public application sharing uses Cloudflare Quick Tunnels;
- the unrestricted Browser visits addresses selected by the user;
- recipes and third-party applications may make outbound requests according to their displayed
  permissions and upstream behavior.

LAN sharing, public tunnels, Tailscale Serve/SSH and API keys are disabled until explicitly enabled.
Once enabled, the receiving network/provider can observe the connection metadata and any content
sent through that feature. Cloudless cannot make a third-party web application private if that
application itself is configured to use a remote API.

Durable control-plane state lives under `/var/lib/cloudless`; app/model data may also live in Docker
volumes and `/home/cloudless/Cloudless`. An encrypted `cloudless-backup` contains control-plane
configuration, generated secrets, API-key records and cluster identity, so possession of both the
archive and passphrase is sensitive.

Support bundles are generated locally, are never uploaded automatically and redact known token,
password, authorization, hostname and home-path patterns. They include system/GPU summaries,
health checks, selected non-secret state, recent security-audit events and bounded diagnostic
details. Redaction is defense-in-depth, not a guarantee for arbitrary text placed in names or error
messages. Review the ZIP before sending it outside the machine.

Deleting a chat or application through its own UI follows that application's storage semantics.
Removing a Cloudless application does not imply secure erasure of Docker layers, filesystem blocks,
backups or remote-provider copies.
