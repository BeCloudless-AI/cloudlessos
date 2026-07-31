# CloudlessOS security model

This document defines the security boundaries required before CloudlessOS can be treated as a
supported appliance rather than a development image. It is a threat model, an operational contract,
and a list of residual risks. A feature is not considered safe merely because it is reachable only
from the kiosk.

## Assets

- The host OS, boot chain, package database, NVIDIA driver and system services.
- Model weights, user workspaces, application volumes, Hermes memory and conversations.
- Cloudless API keys, Hugging Face tokens, app passwords, cluster SSH identities and release keys.
- The stable inference endpoint and the integrity of the model/runtime currently served by it.
- Cluster membership, Spark fingerprints, private-fabric configuration and peer state.
- Signed packages, manifests, reviewed profiles, release metadata and rollback generations.

## Actors and trust levels

| Actor | Trust | Intended authority |
|---|---|---|
| Local kiosk user | Physical-user trust | Operate Cloudless, install reviewed apps, choose models and use explicitly confirmed settings |
| Local terminal/SSH administrator | Full host administrator | Run unrestricted commands after OS authentication; changes are not automatically considered Cloudless-managed |
| LAN client | Untrusted until authenticated | Use only an explicitly shared app or scoped gateway route |
| Public API client | Hostile by default | Use allow-listed inference/agent routes through a scoped, rate-limited key |
| Containerized app | Partially trusted | Access declared volumes, loopback-published ports, the Cloudless network and declared GPU devices only |
| Local unreviewed recipe | Untrusted code | Must not execute until its permissions are understood and a constrained execution boundary exists |
| Cloudless-reviewed profile | Package-authenticated code | Execute only its exact pinned source/image identity and declared permissions |
| Cluster peer | Authenticated appliance, not implicit root | Join only after fingerprint enrollment; operate only on the explicitly selected subset |
| Update infrastructure | High trust | Publish atomically signed, architecture/platform-scoped artifacts; never receive the archive private key |

## Trust boundaries

1. **Browser to local daemon.** The kiosk is not an authorization boundary. Mutating or dangerous
   routes require explicit action headers, validate all structured input, and remain bound to
   loopback unless an exposure controller deliberately changes that state.
2. **Daemon to containers.** Published ports bind to loopback. LAN/public access is provided by
   separate removable sidecars. Inference containers receive neither Cloudless gateway keys nor
   release credentials.
3. **Local daemon to Hermes.** Hermes receives a random daemon-owned credential stored mode 0600.
   The public gateway exposes only allow-listed inference/agent routes, never Hermes administration.
4. **Host to cluster peer.** Enrollment records the SSH host fingerprint and Spark identity.
   Rebinding an address must prove the same fingerprint. Operations use an exact selected subset and
   record per-node evidence.
5. **Release client to update origin.** A pinned archive key verifies repository metadata. Platform,
   architecture, channel and version are checked before installation. Publication changes the public
   generation only after verification succeeds.
6. **Terminal to managed state.** The terminal is intentionally unrestricted after Linux login.
   Files or containers changed there are “local/unmanaged” until imported through a validating
   Cloudless registration workflow. Package-owned files are never overwritten by custom-engine
   registration.

## Required controls

### Credentials

- No product image or manifest may contain a real token, private key or fixed administrator
  password.
- App passwords are generated from cryptographic randomness, stored below
  `/var/lib/cloudless/secrets` with directory mode 0700 and file mode 0600, and resolved only by the
  local settings endpoint.
- Internal OpenAI-compatible app clients receive separate stable, generated values instead of a
  shared default string. These values satisfy private client configuration; they are not a
  substitute for application or public-gateway authentication.
- Open WebUI creates no default administrator. Public exposure is refused until account
  authentication is enabled.
- Gateway keys are displayed once and stored only as hashes. Hugging Face and cluster credentials
  use dedicated private files or one-shot request handling and are excluded from diagnostics.
- One-time Spark administrator passwords are passed to `sshpass` through an inherited, short-lived
  file descriptor. They never enter the child process argument vector or environment, and any
  accidental password echo in command failure output is redacted before it reaches a job or log.
- Managed model downloads, first-model promotion, sandboxed container recipes and the signed
  legacy recipe adapter mount the owner-only Hugging Face credential as a fixed read-only secret
  file and set only `HF_TOKEN_PATH`.
  The engine broker admits exactly that host path and container destination, rejects symlinks and
  group/world-readable files, and never persists the token value in Docker environment metadata.
- Release credentials live outside the repository and outside R2. The archive signing key remains
  offline except for an intentional signing operation.

### Network exposure

- Application and inference ports bind to `127.0.0.1`.
- LAN/public changes require explicit confirmation headers and create separate sidecars.
- A public app must prove authentication is enabled before a tunnel starts.
- Gateway routes are allow-listed, scoped per key, rate-limited per key and source, and audited
  without request/response content.

### Recipes and containers

- The UI must show source revision, image digest, commands, remote-code use, filesystem/network/
  device access and selected cluster nodes before execution.
- Mutable source branches and mutable image tags cannot be promoted to a reviewed identity.
- The offline release trust inventory binds every reviewed recipe and distributed compatibility
  profile to its canonical metadata digest, exact source commit and Cloudless archive fingerprint.
  Its detached signature and signed-release hash can be verified without running CloudlessOS.
- Unreviewed code runs only through the constrained managed-container adapter. Raw Docker-socket
  access is root-equivalent and is never treated as a sandbox; the source-script adapter remains
  restricted to exact package-reviewed recipes.
- Every operation owns its containers, processes, ports, staged files and cache artifacts and must
  support idempotent abort, cleanup and recovery.

### Host services

- Services use `NoNewPrivileges`, private temporary directories, read-only home/system views and
  explicit writable paths wherever their duties permit.
- Root daemons are acceptable only for operations that cannot be delegated yet; every root-only
  path is an acknowledged migration target for a narrowly scoped privileged helper.
- Power control, package/driver update triggers and Tailscale installation are delegated to
  `cloudless-privileged`. Its Unix socket is group-protected and every connection is checked with
  `SO_PEERCRED`; its wire protocol accepts only fixed action identifiers, rejects unknown JSON
  fields and has no command or generic argument field. The sole typed value is an IANA-validated,
  length-bounded timezone for a fixed `/usr/bin/timedatectl set-timezone` action. Every other
  identifier maps to a fixed `/usr/bin/systemctl` invocation.
- Local Spark fabric configuration also crosses the broker. Requests contain exactly two
  restricted interface identifiers and a node position from 1 through 8. The broker generates
  Cloudless's fixed netplan document, refuses symlink/non-regular replacement targets, applies it
  with fixed absolute binaries and removes only the two reserved `10.100.0.0/24` and
  `10.100.1.0/24` address families during cleanup. It never accepts YAML or shell text.
- The terminal binds to loopback and delegates authentication to `/bin/login`.

## Logging and privacy

Security logs record time, actor/key identifier, action, target, outcome and duration. They must not
record prompts, model responses, authorization headers, passwords, provider tokens, private keys or
raw environment values. Diagnostics pass through the shared redactor and are safe to attach to a
support case only after the user reviews the generated archive.

## Residual risks

- The Docker daemon socket is root-equivalent. Ordinary engine operations and exact reviewed
  recipe operations now cross the peer-authenticated `cloudless-engine` broker, and the packaged
  `cloudlessd` unit neither references that socket nor executes `/usr/bin/docker`. Reviewed recipe
  requests are admitted only after the broker independently validates the signed operation,
  revision, Git object hashes, working directory, environment, Docker argument shape and rendered
  Compose plan.
- Cloudless container launches now pass a mandatory broker-oriented policy that rejects unmanaged
  names, Docker-socket/host-root mounts, paths outside Cloudless-owned storage, symlink traversal,
  hostile security options and unbounded runtime controls. All managed operations cross the
  separate peer-authenticated engine service. The packaged `cloudlessd` now runs under its
  dedicated system identity: model data lives in Cloudless-owned host storage, and display/input
  operations cross an exact-peer-authenticated desktop-session helper. Browser opens use atomic,
  typed request files. The daemon has no Docker socket, `.Xauthority`, arbitrary command channel
  or membership in the desktop account's control-broker group. Fresh-install, upgrade and rollback
  qualification of this boundary on supported physical targets is still outstanding.
- An authenticated terminal administrator can intentionally alter the host. Cloudless can detect
  and label divergence but cannot make root harmless.
- Quick tunnels depend on a third-party relay and must never be enabled silently.
- GPU drivers, firmware, browser snaps and upstream container images remain supply-chain inputs.
- The Tailscale package repository key is fingerprint-pinned in the signed Cloudless package.
  Vendor key rotation therefore requires an intentional Cloudless update; an unexpected replacement
  fails closed instead of being trusted automatically.
- A local physical user can see content displayed in the kiosk. CloudlessOS is not a multi-user
  desktop security boundary.

## Security release gate

A release fails when secret hygiene, host-service hardening, external repository trust,
signature verification, platform isolation, SBOM generation, vulnerability policy, recipe
permission tests, diagnostic redaction or backup/restore rehearsal fails. Critical/high findings
require remediation or an explicit time-bounded security exception; an ordinary known-limitations
note is not sufficient.

Every release also publishes a detached-signed security-readiness descriptor. For 1.0+ stable this
must prove a monitored public disclosure route, a named escalation role, a response target of at
most three business days and operator verification within 30 days. Missing, stale, future-dated,
unmonitored, credential-bearing or structurally unknown evidence fails before signing.
