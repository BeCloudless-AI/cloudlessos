# CloudlessOS master roadmap

This is the execution roadmap for turning the current CloudlessOS prototype into a dependable
desktop distribution for ordinary NVIDIA systems and DGX Spark clusters. It is deliberately
ordered by dependency: reliability and recoverability come before adding more applications,
models or community features.

For the detailed recipe transaction plan, use
[`RECIPE_RELIABILITY_ROADMAP.md`](./RECIPE_RELIABILITY_ROADMAP.md). That document is the source of
truth for recipe Check, preparation, launch, abort, rollback and cluster recovery.

## How to use this roadmap

- Work from the first unfinished milestone unless an item is explicitly marked parallel-safe.
- A feature is not done when the UI appears to work. Its automated tests, restart behavior,
  cleanup behavior and supported-hardware checks must also pass.
- Update the checkbox and its implementation note in the same change that completes an item.
- Never publish from an unverified working tree. Run the architecture matrix, package checks and
  the relevant physical-machine tests first.
- Keep one source tree and one release generation for AMD64 and ARM64. Platform behavior belongs
  behind capability flags, not in divergent frontends.

## P0 — Make inference operations trustworthy

This milestone addresses the repeated recipe failures, port conflicts, indefinite startup state,
misleading progress and incomplete abort behavior. No new recipe or inference-engine feature should
ship before this milestone is complete.

- [x] Finish custom-build image preflight so Check proves the exact produced image, architecture,
  entrypoint and immutable digest before the active engine is stopped.
- [x] Add a bounded real NCCL/bootstrap probe and repeat it immediately before switching engines.
- [x] Add a lightweight OpenAI-contract probe for command, bind address, health route and the
  required `cloudless` served-model alias.
- [x] Enforce the stable Cloudless API port and `cloudless` model identity centrally for every
  managed engine and recipe; recipes may not bypass the gateway.
- [x] Resume safe preparation after daemon restart and roll back unsafe switching/starting phases.
- [x] Implement recipe tombstoning so deleting a recipe cannot orphan its containers, files,
  claims or peer processes.
- [x] Prove Abort stops local and remote work, releases ports and accelerators, and leaves reusable
  downloads intact.
- [x] Complete failure-injection coverage for download, transfer, build, start, health, proxy,
  promotion, state save, daemon restart and node loss.

Done gate:

- Every injected failure ends with either the previous model healthy or Cloudless explicitly
  unloaded, with no silent resource ownership.
- Recipe progress survives modal closure, page reload and daemon restart.
- The UI identifies the phase and Spark causing a failure and exports a secret-free diagnostic ZIP.
- The full requirements in `RECIPE_RELIABILITY_ROADMAP.md` pass on AMD64, ARM64 and physical Sparks.

## P1 — Harden the signed distribution and update pipeline

- [x] Make the single release command build, test, sign, atomically publish and publicly verify both
  architectures without requiring repeated credentials or auxiliary scripts.
- [x] Reject test keys, test versions, fake endpoints, missing platform contracts and dirty or
  ambiguous repository roots before publication begins.
- [x] Keep AMD64 and ARM64 package generations aligned while allowing explicit platform capability
  packages and version-locked features.
- [x] Add immutable release manifests containing package hashes, architecture, channel, changelog,
  compatibility contract and rollback target.
- [x] Test interrupted uploads and guarantee that clients see either the previous complete release
  or the new complete release, never a mixed repository.
- [x] Add an update UI with changelog, accurate progress, restart requirement, retry guidance and
  post-restart confirmation.
- [ ] Qualify package upgrade and rollback while a model is active and while a durable preparation
  operation is interrupted.
- [ ] Rotate all credentials that have appeared in chat or terminal output and document a
  least-privilege secret-management procedure.

Implementation status (July 31, 2026):

- `distro/scripts/release.sh` is the sole operator command. It loads a private per-user environment,
  executes all source, architecture, package, browser and publication gates, enters the protected
  signing environment, publishes both architectures and verifies the public result.
- Package indexes and the signed changelog manifest now use immutable SHA-256 URLs. The updater
  verifies `InRelease` first, derives the manifest's signed hash and size, and only then downloads
  that exact by-hash object; it no longer races the mutable channel alias.
- The atomic publication test injects a failure at every individual object-store upload. After each
  interruption it verifies that the visible signed generation is exactly the old or new `InRelease`
  and that every referenced package index and changelog object is present with the signed hash and
  size.
- Production preflight runs before builders or publishers. It requires the exact committed archive
  fingerprint and Cloudless update identity, production version/channel, account-scoped Cloudflare
  endpoint, credential shapes, complete AMD64/ARM64/DGX contract, canonical clean Git root and the
  complete release-gate set. Test identities, placeholder destinations and environment-selected Git
  roots fail closed; an isolated test suite exercises each rejection.
- Signed `cloudless.release.v2` manifests inventory all six packages on both architectures, including
  exact version, pool filename, SHA-256, byte size, changed state and any retained rollback object.
  Their compatibility contract is bound to the same matrix hash and target set as the release-gate
  attestation. The publisher checks every current package locally and every rollback package through
  the public endpoint; the updater rejects the wrong channel, platform, architecture or source
  identity before showing release notes.
- Settings renders the verified changelog before installation, displays overall progress only while
  the package transaction is active, exposes actionable failure text and retry, requests restart
  when required and converts that state to a confirmed successful update only after observing a new
  boot ID. API, persistence and embedded-interface contract tests cover these states.
- Immediately before package installation, the updater records any ready engine and every
  non-terminal durable recipe operation. A new generation is accepted only when the same engine is
  still ready and every captured operation remains present and non-failed after `cloudlessd`
  restarts. Otherwise the updater restores the prior packages and applies the same continuity gate
  to the rollback. Unit tests cover preserved, failed and disappeared workloads; the destructive
  package rehearsal on physical targets remains outstanding.
- A required `secret-hygiene` release gate scans all tracked and non-ignored source files without
  echoing matches. The least-privilege storage, revocation and local environment procedure is in
  [`SECURE_RELEASE_CREDENTIALS.md`](./SECURE_RELEASE_CREDENTIALS.md). Credentials previously exposed
  outside the repository still require operator-side revocation and replacement before P1 closes.

Done gate:

- One command publishes a verified release for both architectures.
- A VM, a generic NVIDIA machine and a DGX Spark independently detect, install, restart and confirm
  the same release generation.
- Publication failures cannot corrupt the live APT repository.

## P2 — Qualify installation and first boot

- [x] Keep one branded AMD64 installer ISO for generic hardware and one documented package-layer
  installation path for NVIDIA's supported DGX OS base.
- [x] Add pre-install checks for architecture, disk, firmware mode, network and supported GPU path.
- [ ] Verify graphical boot without manual TTY switching on VirtualBox and representative physical
  NVIDIA systems.
- [x] Make LightDM/kiosk startup ownership, browser profile permissions and first-boot recovery
  deterministic.
- [x] Play the Cloudless splash on every boot without delaying readiness; automatically reduce or
  disable costly animation on DGX Spark.
- [x] Provide recovery logs and a repair path when the GUI, Docker, cloudlessd or the display stack
  fails.
- [ ] Validate shutdown/restart, locale, timezone, resolution, zoom and virtual-keyboard behavior
  from a clean install.

Done gate:

- Ten consecutive install/boot/restart cycles require no console intervention.
- A failed service produces an actionable recovery screen instead of a permanent black screen.

Implementation status (July 31, 2026):

- The AMD64 ISO runs a read-only preflight before Subiquity can partition storage. Unsupported
  architecture and undersized disks fail closed; BIOS/UEFI, network and NVIDIA/virtual/no-GPU paths
  produce explicit results. Hermetic tests cover supported, degraded and rejected machines.
- The ISO builder now admits exactly six packages whose Debian architecture is `amd64`; stale ARM64
  release candidates cannot leak into its glob. A real 0.2.6-dev hybrid image passed structural
  inspection plus QEMU BIOS and UEFI framebuffer smoke tests. Those tests now compare the upper
  framebuffer against the exact installer background extracted from the ISO; arbitrary nonblank
  OVMF or kernel text can no longer pass as a branded Cloudless boot.
- The DGX package-layer installer verifies ARM64 Spark identity, writable/free storage, firmware,
  network, signed repository, driver, Docker and CDI before changing the system.
- If the local API misses its startup deadline, the kiosk opens a packaged recovery screen instead
  of an empty Openbox background. It retries automatically and directs the operator to
  `cloudless-repair --repair` and the expanded secret-free `cloudless-diagnostics` report.
- The boot audit now waits on the complete default-target, LightDM, X socket, Cloudless session,
  kiosk-browser, profile-owner and API chain. It atomically records the exact failed probe plus a
  bounded 20-boot history and consecutive-healthy count. Repair restarts LightDM before checking
  the recovered display and waits for the same ownership chain instead of relying on a fixed sleep.
- The embedded launch animation is keyed to the kernel boot ID, so browser reloads cannot replay it
  while every new OS boot does. The desktop is initialized underneath it first; DGX Spark disables
  the costly animation by default and exposes the existing per-display override with a warning.
- Package validation proves the recovery page and repair command exist in both architecture
  generations, and now also proves the boot auditor in both architecture packages. Physical repeated
  boot/install qualification is still required before the remaining P2 items and the P2 done gate
  can close.
- The disposable installed-session rehearsal now drives the packaged daemon, desktop agent, browser,
  terminal and a protocol-compatible privileged broker together. It proves virtual typing, browser
  profile reuse, terminal continuity, unconfirmed display rollback after daemon restart, confirmed
  resolution persistence, UAE-to-`Asia/Dubai` timezone dispatch, and both restart and shutdown broker
  actions without powering off the test container. AMD64 and QEMU/ARM64 package lifecycle jobs run
  this same contract; clean-install physical evidence is still required before checking the item.

## P3 — Stabilize the Cloudless desktop experience

- [x] Establish shared design tokens and reusable controls for modals, buttons, cards, icons,
  spacing, typography, focus, loading, errors and reduced motion.
- [x] Keep Settings and other primary modals at predictable dimensions with full-width content and
  responsive layouts from 720p through high-DPI displays.
- [x] Finish automatic scaling plus user-selectable zoom up to 300%, resolution selection and safe
  display rollback.
- [x] Ensure apps appear in Settings, the launcher and pinning surfaces only when their lifecycle
  state permits it; uninstalled apps cannot be pinned.
- [x] Keep external pages inside the Cloudless window model. Browser and terminal windows retain
  state, tabs, stacking and launcher presence when minimized.
- [x] Make the Cloudless virtual keyboard work for native inputs and, where technically permitted,
  same-origin embedded content; explain cross-origin limitations rather than substituting another
  keyboard.
- [x] Finish the guided tour with one performant blur layer, clickable highlighted targets,
  action-gated Next controls and reduced-motion support.
- [x] Add visual-regression screenshots for the desktop, Settings, Model Manager, Recipe Library,
  update flow, cluster wizard and power modal at supported breakpoints.

Done gate:

- No control changes size, wraps its icon or blinks during polling/streaming.
- Keyboard-only navigation, focus trapping, reduced motion and major contrast states pass an
  accessibility review.
- The same frontend build adapts through backend capabilities on generic and Spark systems.

Implementation status (July 31, 2026):

- Display scaling initializes before the interface paints, follows screen dimensions automatically,
  and supports persistent manual zoom from 80% through 300%.
- Resolution changes are temporary until the user confirms the new mode. The backend—not merely the
  browser—restores the previous validated XRandR mode after a bounded countdown. The rollback record
  is written atomically before changing the display, survives a `cloudlessd` crash, retries a failed
  restoration and is removed only after confirmation or successful rollback. API, UI-contract and
  race tests cover confirmation, timeout and daemon-restart recovery.
- `scripts/visual-regression.ps1` produces one validated, checksummed capture set from the real
  frontend CSS, including 720p/1080p/1440p desktops plus Settings, Model Manager, App Launcher,
  Assistant, model-startup, API-access, inference, Recipe Library, Update Center, cluster wizard
  and power-confirmation surfaces. CI now runs the complete 17-surface matrix and compares a
  deterministic 32×32 luminance signature for every capture against a reviewed checked-in baseline;
  a mean difference above 3 fails the job while preserving the screenshots and comparison report.
  Baseline changes require an explicit `-UpdateBaseline` run, and a source-policy test rejects a
  missing surface, malformed signature, loosened threshold, Quick-only workflow or missing artifact.
- Shared spacing, radii, focus, motion, window-geometry and control tokens now back the primary
  windows. The base `.btn` contract keeps icon and text on one stable line with a fixed minimum
  height, non-shrinking SVG and visible keyboard focus.
- Settings derives application pages only from running containers, pin creation requires a running
  container, and the pins API omits saved shortcuts as soon as their application stops. Backend and
  embedded-frontend contract tests cover all three boundaries.
- Primary Settings and workspace windows use shared width/height tokens, full-width page content and
  viewport-bounded dimensions. The regression capture set exercises the desktop and primary windows
  at 720p, 1080p and 1440p so spacing and wrapping failures are visible before release.
- External links and `_blank` requests route to the persistent Cloudless Browser process instead of
  escaping through ad-hoc windows. Its profile, running/minimized state, dock task and above-kiosk
  layer survive desktop interaction. Terminal minimize only hides its persistent iframe set; tabs
  are destroyed solely by their explicit close controls.
- The Cloudless virtual keyboard directly edits native inputs and uses the authenticated system-input
  bridge for focused embedded application and terminal frames, without installing or launching a
  second on-screen keyboard. Cross-origin pages remain opaque to browser JavaScript, so Cloudless
  relies on actual frame focus and system key delivery; a page that refuses focus cannot be
  introspected or overridden safely.
- The guided tour uses one masked blur sheet plus transparent input guards. Only the highlighted
  action remains clickable, action steps omit Next until the required click occurs, explanation
  steps lock the opened surface (including Assistant input), and reduced-motion mode removes its
  transitions and pulses.

## P4 — Make models and engines predictable

- [x] Replace heuristic model-fit claims with manifest-backed requirements per quantization,
  runtime, architecture and cluster topology; never equate parameter count with loaded memory.
- [x] Report unified memory, reservations, cache and real per-node headroom accurately for DGX
  Spark clusters.
- [x] Make model download/load/unload durable background jobs with exact bytes, per-node stages,
  progress, ETA, safe abort and resumable caches.
- [x] Keep desktop state truthful: Metrics only accompanies a loaded model, Load opens Model
  Manager, and Load becomes Abort while loading.
- [x] Centralize engine installation and update state so vLLM/SGLang versions do not remain falsely
  marked stale after a successful update.
- [x] Support locally registered engine builds, including SM121-optimized vLLM, through explicit
  selectable profiles with contract checks and rollback.
- [x] Make the remote model/engine manifests signed, versioned and cacheable with a well-defined
  offline fallback.

Done gate:

- Load status reflects the actual process and API, not inferred timers.
- Switching among managed, custom and recipe runtimes preserves the same Cloudless API contract.
- Model compatibility statements are reproducible from stored evidence.

Implementation status (July 31, 2026):

- Language-model compatibility now comes only from an explicit fit profile matching the selected
  engine, CPU architecture, Cloudless platform, memory type, node count, sharding mode and requested
  context. Model names, parameter counts, quantization labels and repository sizes are never used to
  invent loaded-memory requirements.
- Fit results distinguish measured evidence, reviewed requirements and missing evidence. Missing
  evidence produces `unknown` / “Not reviewed” throughout the API, Model Manager and Cloudless
  Assistant instead of a plausible-looking estimate.
- The packaged model catalog carries reviewed single-node profiles, while the qualified Qwen
  3.6 35B A3B DGX Spark path carries a measured two-node vLLM profile. The generic cluster machinery
  supports larger subsets, but no three-to-eight-node model claim is made until each topology is
  independently qualified. Cluster requirements are evaluated per node rather than against
  misleading aggregate memory.
- Hosted model manifests may provide launch-affecting fit profiles only with schema version 2 or
  newer and a verified detached archive signature. Unsigned manifests may still contribute display
  metadata, but their fit profiles are stripped and packaged profiles remain authoritative.
- Model, fit-engine, manifest, Assistant, Hugging Face import, API and embedded-frontend regression
  tests cover false parameter-based claims, mismatched runtime/topology, signed-profile trust and
  truthful unknown states.
- DGX Spark telemetry now separates physical unified memory, Linux `MemAvailable`, reclaimable
  cache, current system use, the explicit CloudlessOS safety reserve, AI workload capacity and
  launch headroom. Cache is never double-counted because Linux already includes its reclaimable
  portion in `MemAvailable`.
- Cluster totals are built from live telemetry for every enrolled node. Compatibility uses the
  least-capable node's capacity and current headroom, while Model Manager separately labels physical
  aggregate memory, AI capacity and memory available now. If a peer cannot report memory, the UI
  says telemetry is updating instead of multiplying the local Spark's memory into a false total.
- Model Manager downloads now journal their model, phase, exact bytes and timestamps in the
  atomically written Cloudless state. A daemon restart recreates the background job with a stable
  per-repository helper identity, and Hugging Face resumes verified chunks from the shared cache.
  Cancel stops the helper but deliberately retains partial chunks; Uninstall remains the explicit
  action that removes model data.
- Load, unload, engine switch, model switch, restart, cluster fallback and Abort now share a durable
  inference-operation journal containing the desired runtime, verified rollback target, exact
  progress and per-node stages. Superseded writers cannot overwrite a newer Abort. On daemon restart,
  provisioning reconciles the journal against Docker's supervised process; a failed local candidate
  restores the previous runtime, while a distributed or recipe-owned target fails safely as selected
  but unloaded instead of being guessed back into service.
- Distributed launch progress reports the coordinator and every worker separately. Each Spark exposes
  download bytes, chunk/download state, weight-loading state, percent and an ETA calculated from its
  measured transfer rate. The desktop banner renders those durable stages and reports a recovery
  error explicitly instead of holding an inferred percentage forever.
- Desktop and Model Manager readiness is driven by the engine API: the desktop Metrics capsule is
  hidden unless inference is actually ready (the static sidebar Metrics entry remains available),
  an unloaded model's Load action opens Model Manager, and an active launch exposes Abort until its
  registered job finishes. Polling continues during launch so the UI cannot freeze on a stale action.
- Managed vLLM and SGLang updates resolve one immutable digest target, pull and verify that exact
  reference, and persist downloaded/active artifact identity. Active engines are launched from that
  same reference and must prove both the stable Cloudless API contract and expected container digest
  before the update job can succeed; inactive downloads no longer reappear as perpetually stale.
- Local engine registration now creates an explicit, selectable profile bound to the inspected image
  ID and host architecture, with a versioned inherited launch/API contract and visible validation
  state. First activation must pass Cloudless's stable OpenAI endpoint/model-alias probe; failed
  profiles are marked with the contract error and the durable inference transaction restores the
  previous verified runtime (or leaves it safely unloaded if automatic restoration is not valid).
- App, language-model and diffusion manifests now use the Cloudless updates origin and one detached-
  signature verification path. Supported schema versions are explicit; unknown versions fail closed.
  Only decoded, verified production payloads enter an atomic disk cache, their signatures are
  reverified on every cold-start fallback, and offline use is bounded to 30 days. The release
  pipeline signs, publishes and publicly verifies all three manifests as release artifacts.

## P5 — Productionize DGX Spark clustering

- [x] Replace cable heuristics with understandable interface/link detection and distinguish
  physical link, IP reachability, SSH login, fabric health and distributed-runtime health.
- [x] Make cluster connection/disconnection a durable wizard with progress, rollback, credentials
  validation and support bundles.
- [x] Support exact compatible node subsets from two through eight Sparks and expose every selected
  node's utilization, memory, storage, temperature, power and link status.
- [x] Prevent disconnect while distributed work owns remote resources unless cleanup is completed
  or explicitly left as a visible degraded obligation.
- [ ] Test packet loss, address changes, coordinator loss, worker loss, reboot, role reversal and
  partial cleanup at every inference lifecycle phase.
- [x] Add the DGX Dashboard entry and NVIDIA branding only when the DGX capability is detected.
- [x] Maintain a validated recipe/profile library for supported distributed configurations rather
  than assuming arbitrary upstream scripts are production-ready.

Done gate:

- Two-, three- and selected-subset clusters repeatedly connect, run, stop, reboot and disconnect
  without stranded processes or repeated model downloads.
- Node loss is visible and recoverable without falsely reporting the cluster or model as healthy.

Implementation status (July 31, 2026):

- Cluster status now reports five independent layers: physical ConnectX links, normal management
  network reachability, restricted-key SSH, both private fabric paths, and the distributed worker
  runtime. Every worker has its own layered result and the dashboard presents the aggregate layers
  separately, so an idle worker is not confused with a broken cable or failed login.
- Fabric health requires zero packet loss across the short verification burst; partial loss is
  reported as attention instead of being treated as healthy merely because one packet arrived.
- Connect and disconnect now run as background jobs with real phases and percentages mirrored into
  the persisted, credential-free cluster operation journal. Closing the modal does not cancel a
  mutation; a daemon restart turns an unfinished mutation into an explicit cleanup obligation rather
  than guessing success. Credentials are validated and retained only in the live job, partial
  mutations preserve the configured cluster plus a rollback-required error, and the journal is
  included in the redacted support bundle.
- Mutation ownership records both PID and Linux boot ID, so PID reuse after a daemon or machine
  restart cannot make an interrupted operation look active. Connect/disconnect journals retain
  per-node phase and cleanup evidence. Table-driven tests interrupt every mutation phase and verify
  that unfinished work becomes a visible rollback obligation; repeated physical packet-loss,
  coordinator-loss, reboot and cleanup qualification remains part of the unchecked hardware matrix.
- Management-address recovery is fingerprint-bound: the operator supplies a new address, Cloudless
  proves it is the already-enrolled machine using the stored SSH host fingerprint, revalidates ARM64
  DGX identity and credentials, and updates only management routing. Role reversal fails closed and
  requires an explicit disconnect/re-enrolment instead of silently changing coordinator ownership.
- Managed distributed inference can now use an exact persisted subset of one to seven workers plus
  the coordinator. The coordinator tensor-parallel size, worker launch, and preparation progress all
  consume the same selection. The dashboard distinguishes selected and standby nodes and reports
  live utilization, unified-memory headroom, storage, temperature, power, and physical-link state
  for the coordinator and every worker.
- Selection changes and disconnects fail closed while an inference lifecycle operation owns cluster
  resources. Recipe operations must reach verified cleanup first; partial disconnect mutations stay
  recorded as rollback-required obligations instead of being reported as a healthy disconnection.
- The API recovery contract now cross-tests packet loss, management-address change, coordinator
  loss, selected-worker loss, coordinator reboot, rejected role reversal and partial cleanup against
  every durable non-terminal inference phase. Every combination fails readiness closed, prevents a
  cluster mutation and preserves the durable job ID plus an Abort action for load/switch/restart.
  An abort or unload already in progress cannot recursively expose Abort. This matrix is repeated in
  the race-enabled lifecycle soak; the roadmap item remains open until the same phase/failure matrix
  has recorded physical multi-Spark evidence.
- Automatic distributed inference now passes through a versioned reviewed-profile allow-list. The
  initial profile pins Qwen 3.6 35B A3B to its immutable Hugging Face revision, vLLM, ARM64 DGX Spark
  unified memory, the measured two-node topology, 32K context and the stable `cloudless` model alias.
  Launch also requires the architecture-specific digest-pinned engine image from the signed
  manifest. Unknown models, engines, artifacts and node counts fail before runtime mutation.
- The DGX Dashboard navigation and the discrete NVIDIA badge are both gated by the server-provided
  `dgx-appliance` capability. Generic CloudlessOS installations cannot reach Spark-only APIs and do
  not render the DGX navigation or branding.

## P6 — Integrate Hermes Agent safely

- [x] Make Hermes a non-removable platform capability and make Reset restore the Cloudless-managed
  model/provider configuration.
- [x] Route model questions and install requests through Cloudless model-fit and Model Manager APIs,
  not Hermes-specific install guesses or fabricated skills.
- [x] Eliminate repeated tool loops and streaming UI flicker; display one stable incremental answer.
- [x] Expose gateway sharing separately for local-network and public access, disabled by default,
  with authentication, scope, audit logs and explicit risk warnings.
- [x] Show “Powered by Hermes Agent” without making topology or implementation details part of the
  conversation contract.
- [x] Add prompt/tool regression tests for hardware-fit questions, model installation, engine
  selection, abort, unavailable tools and permission refusal.

Done gate:

- Hermes cannot silently bypass Cloudless lifecycle, security or model-management policy.
- LAN/public exposure passes authentication, authorization, rate-limit and secret-storage review.

Implementation status (July 31, 2026):

- Hermes is a preinstalled core capability and cannot be uninstalled through either the API or UI.
  Reset replaces only its Cloudless-owned model/provider block, restarts it with a private
  install-generated loopback credential, and preserves memory, sessions, messaging configuration
  and workspace data.
- Compatibility, download, installation and switching questions are intercepted before Hermes.
  Answers use live Model Manager fit evidence and return a Model Manager action; unknown artifacts
  remain explicitly unreviewed. The system prompt prohibits skills, shell/package workarounds,
  conversational-consent execution and repeated retries after denial or unavailable tools.
- The assistant updates one existing DOM message at animation-frame cadence during streaming and
  performs a single formatted replacement at completion. Tool progress updates the same message;
  regression coverage rejects whole-thread rerenders inside the token loop.
- The shareable gateway exposes separate model and agent URLs but only allow-listed inference
  routes. Hermes administration and its dashboard remain loopback-only. Every request requires a
  model, agent or combined scoped key; per-key and per-source token buckets limit abuse.
- LAN and public exposure are off until the user confirms a risk-specific Cloudless dialog, and the
  mutation API requires a matching explicit-action header. Exposure is refused until a scoped key
  exists. A rotating mode-0600 JSONL audit records authentication failures, rate limits, key
  lifecycle, exposure changes and request outcomes without prompts, responses or secrets; recent
  entries and the active policy are visible in Settings.

## P7 — Applications and advanced-user workflows

- [x] Treat applications as individual catalog entries with clear names, descriptions, categories,
  dependencies, storage requirements and lifecycle state; use packs only for genuine multi-app
  bundles.
- [x] Keep optional apps absent from a clean install and provision them reproducibly on demand.
- [x] Verify launch URLs, health checks, uninstall cleanup, persistence and default-model integration
  for every catalog app.
- [x] Provide unrestricted terminal access as an explicit advanced-user surface with persistent
  floating windows and tabs, without letting terminal experiments corrupt managed-state claims.
- [x] Integrate Tailscale with clear identity, connection and disconnect states while preserving a
  normal terminal fallback.
- [x] Add managed browser lifecycle, downloads and update policy appropriate for a kiosk OS.

Implementation note: SearXNG, Perplexica and n8n are first-class applications rather than
single-component pseudo-packs; only the genuine Voice and Private Knowledge multi-service bundles
remain. Visible apps now require complete launcher, resource, health and persistence metadata.
Direct installs run their post-install integration, and failed installs roll back only dependencies
introduced by that request. Uninstall removes the app plus LAN/public sidecars and downloaded image
while retaining named volumes and user state. Tailscale mutations require explicit action headers;
the browser reports its persistent profile, Downloads location and OS/Snap-managed update policy.
The executable lifecycle matrix drives every supported non-engine catalog entry through install,
dependency resolution, post-install integration, update, health-checked restart, uninstall,
sidecar/image cleanup and persistent-data retention. Custom engine registration continues to store
only a validated local image identity and managed runtime profile; it does not replace
package-owned files.

Done gate:

- Every published app passes install, launch, restart, update, uninstall and storage cleanup tests.
- Advanced local builds can be registered as managed engines without replacing package-owned files.

## P8 — Security and operations

- [x] Define the threat model for local kiosk users, SSH administrators, LAN clients, public API
  clients, recipes, containers and update infrastructure.
- [ ] Remove plaintext/default credentials and use scoped OS identities, protected keyrings and
  least-privilege services.
- [x] Sandbox unreviewed recipes and show their filesystem, network, device and host permissions
  before execution.
- [x] Sign packages, manifests, reviewed recipes and compatibility profiles; verify all trust roots
  offline where possible.
- [x] Add audit logs for updates, engine changes, recipe execution, cluster administration, key
  generation and remote exposure without logging secrets.
- [ ] Establish vulnerability scanning, dependency updates, SBOM generation, backup/restore and
  incident-response procedures.

Implementation status (July 31, 2026):

- [`SECURITY.md`](./SECURITY.md) defines protected assets, actors, trust boundaries, current
  controls, residual risks and the security release gate for local, remote, container, recipe and
  update-infrastructure threats.
- Application manifests cannot contain fixed administrator passwords. Applications needing a
  bootstrap secret use install-generated, mode-0600 managed secrets; local and public exposure
  require separate explicit-action headers, and public exposure fails closed until the application
  proves authentication is enabled.
- Credential-shaped catalog environment values are now rejected unless blank or explicitly
  machine-managed. Open WebUI, Data Designer, OpenClaw, Hermes and Perplexica receive distinct,
  stable per-install model-client values rather than the old shared `cloudless` literal. Embedded
  Hermes configuration resolves its managed value before being written, and an exact legacy
  placeholder is migrated without overwriting a user-supplied provider credential. Stale catalog
  text advertising well-known AI Toolkit and Unsloth passwords has been removed.
- Passwordless Jupyter workbenches are admitted only when the signed catalog classifies them as
  local-only, non-shareable `code-execution-ui` surfaces. Changing LAN/public exposure, removing
  local-only confinement or relabeling the risk now invalidates the manifest before provisioning.
  This preserves unrestricted local research workflows without silently turning a notebook into
  an unauthenticated network service.
- The privileged recipe boundary now fails closed. The existing `source-scripts-v1` adapter can
  execute arbitrary host programs and reach the root-equivalent Docker socket, so Check and Run
  admit it only when the complete recipe exactly matches a profile authenticated inside the signed
  Cloudless package. Editing any executable field removes that provenance immediately. Unreviewed
  recipes remain saveable and inspectable, with requested commands and permissions visible, but
  cannot start a process or image build.
- The `managed-container-v1` adapter is the command-free path for user-authored recipes. It accepts
  only an immutable image digest, immutable model revision, one local vLLM node and a small
  allow-list of non-contract engine toggles. Cloudless synthesizes the entrypoint and complete
  launch command, fixes the model alias and private loopback port, mounts only the named model
  cache, gives the container no host paths or Docker socket, drops every Linux capability, enables
  `no-new-privileges`, uses a read-only root with bounded tmpfs and process count, and journals
  container ownership for Abort, rollback, stop and boot recovery. The Recipe Builder shows these
  device, network, storage and host restrictions before Check or Run.
- The one-command release now generates an SPDX 2.3 SBOM for the exact 12-package AMD64/ARM64
  generation and Go dependency graph. The SBOM is reproducible for a source commit, rejects a
  partial package matrix, is detached-signed as an immutable release artifact and is publicly
  verified with the rest of the signed generation.
- A pinned Trivy policy scans both the Go source dependency graph and the extracted filesystem of
  the release packages. Unfixed findings are recorded, while any fixed HIGH or CRITICAL finding
  fails the release. Both reports are validated before the release-gate attestation may be created;
  production preflight and the publisher require the `sbom` and `vulnerability-scan` gates.
- Security-sensitive changes now share one bounded, rotating, mode-0600 JSONL audit. It covers
  system and NVIDIA updates, application and inference-engine updates, model-engine lifecycle,
  recipe checks/runs/stops, Spark cluster administration, API-key lifecycle, gateway access,
  application LAN/public exposure and Tailscale administration. The writer redacts credentials,
  authorization headers and token-shaped values; Tailscale authorization and public tunnel URLs
  are deliberately omitted. Operators can read the unified history through
  `/api/security/audit` (the previous gateway route remains compatible), and support bundles
  include a redacted recent snapshot.
- The packaged `cloudless-backup` command creates an AES-256 encrypted, SHA-256 verified
  control-plane archive without copying model weights or container layers. Restore decrypts and
  validates the complete path allow-list before mutation, retains replaced state under
  `/var/backups/cloudless`, and automatically rolls back a partial replacement. The one-command
  release now requires the `backup-recovery` gate, whose disposable-root rehearsal proves create,
  verify, restore, secret recovery, rollback retention and corrupt-archive rejection.
- Backup creation now validates the source payload before atomically publishing the final archive,
  so unsafe source members or interruption cannot strand a plausible-looking partial backup.
  Create/restore operations share an exclusive machine lock and refuse to race an active Cloudless
  system or NVIDIA update. Restore is exact: protected paths absent from the snapshot are removed
  and retained in a collision-safe rollback directory instead of leaving stale configuration live.
  The rehearsal covers unsafe source links, absent-path restoration, same-second rollback creation
  and overlapping-operation rejection.
- [`BACKUP_AND_INCIDENT_RESPONSE.md`](./BACKUP_AND_INCIDENT_RESPONSE.md) documents offline
  passphrase handling, post-restore validation and response playbooks for compromised API keys,
  public/Tailscale access, recipes/containers, signed updates and Spark peers. Physical VM/Spark
  restore evidence remains unfinished, so the combined operations item above intentionally remains
  open.
- Dependabot proposes weekly, separately reviewable Go module, recipe-indexer npm and GitHub Actions
  updates. Every proposal passes the same vulnerability, multi-architecture, recovery and soak
  gates; automated merging remains disabled.
- Tailscale installation no longer downloads and executes a root shell script. Cloudless writes the
  stable Noble APT source itself, admits exactly the pinned Tailscale archive primary/subkey
  fingerprints and refreshes only that authenticated source before package installation. A new
  mandatory `service-hardening` gate also requires private umasks and `NoNewPrivileges` on every
  package-owned root service where compatible, while explicitly retaining `/bin/login` as the
  loopback terminal's OS-authentication boundary. The least-privilege checklist remains open only
  because previously exposed operator credentials still require out-of-band rotation and the new
  service identities require physical upgrade/rollback qualification.
- Shutdown, restart, system-update checks/applies, NVIDIA-update checks/applies and the Tailscale
  package installer no longer execute `systemctl` inside the HTTP daemon. They cross a mode-0660
  Unix socket into the separately packaged `cloudless-privileged` root service. The broker checks
  Linux peer credentials, admits only root or the dedicated `cloudless-control` group, parses a
  bounded JSON request with unknown fields rejected, and maps a fixed action enum to fixed absolute
  commands without accepting command text or generic arguments. Timezone changes use the only
  typed value in the protocol; it is length-bounded and must resolve through the installed IANA
  timezone database before `/usr/bin/timedatectl` receives it. Protocol, negative authorization, Linux
  build, package-content and systemd-hardening tests cover this first authority slice. The mandatory
  privilege-boundary regression check fails if these delegated actions reappear as direct commands
  in API, remote-access or locale code, or if packaging loses peer authorization, the dedicated
  service identity, or the broker binary/unit. Local Spark netplan creation/removal is now another
  typed broker action: the daemon supplies exactly two conservatively validated interface names
  and a node index from 1 through 8, while the root helper generates the fixed YAML, atomically
  replaces only its package-owned regular file, runs absolute `netplan`/`ip` commands and rolls
  back a failed apply. YAML, paths and commands never cross the socket. Remote peer changes still
  use the enrolled administrator's SSH/sudo session.
- The container-runtime split now has an enforced admission-policy foundation. Every ordinary
  Docker launch is rejected unless its container/network namespace is Cloudless-owned, its ports
  and GPU selection are bounded, its named volumes use the Cloudless namespace, and every host
  mount remains under Cloudless state/workspace/download storage without symlink traversal.
  Docker-socket mounts, arbitrary host mappings, unconfined security options, invalid IPC/ulimit
  controls and host-root/build-context escapes fail closed. The complete curated app catalog is
  executed against this policy in tests. Transient model/download/cleanup helpers now use the same
  typed `RunSpec` contract; no production API or provisioning path may construct a raw `docker run`
  argument array. Named-volume create/inspect/list/remove operations used by ordinary model,
  inventory and garbage-collection paths are typed and namespace-checked as well. Image metadata,
  local digest inventory, managed-container discovery, environment reads, the two admitted Hermes
  settings, NVIDIA-runtime detection and bounded log tails now have typed methods too. The generic
  Docker `Output` channel has been deleted from the engine contract, and the mandatory
  privilege-boundary gate prevents it or raw transient runs from returning. The complete typed
  engine contract now crosses a mode-0660 Unix socket into the separately packaged
  `cloudless-engine` service, reusing Linux peer-credential authorization for the
  `cloudless-control` group. Pull and build progress remain streamed rather than buffered, client
  cancellation closes the broker operation, and unknown request fields fail closed. Packaged
  `cloudlessd` selects this broker and its unit no longer references the Docker socket; unpackaged
  developer binaries retain a direct-Docker fallback. Embedded build contexts use a shared
  Cloudless-owned staging root so private service `/tmp` namespaces cannot break builds.
- Exact reviewed recipes no longer receive a general Docker executable. A compatibility client
  submits the operation ID, pinned recipe revision, working directory, fixed Docker arguments and
  a tiny environment allow-list to `cloudless-engine`. The root broker independently reopens the
  signed operation journal and trust inventory, proves the Git objects and file digests, validates
  the rendered Compose plan, rejects bind mounts, Docker-socket access, privileged containers,
  unapproved devices/capabilities and foreign images, and only then invokes Docker. The API package
  contains no direct `/usr/bin/docker` execution path. Image export is likewise typed and confined
  to an atomic, group-readable archive below `/run/cloudless/transfers`.
- The packaged HTTP daemon now runs as the dedicated `cloudlessd` identity rather than root. Model
  downloads use `/var/lib/cloudless/models-cache`; the root engine broker performs a restart-safe
  hard-link/copy migration from the legacy Docker volume without deleting rollback data. The host
  cache is bind-mounted through the typed engine policy, recursively preserves setgid group
  inheritance and exposes only read-only hard-linked views to the desktop account.
- XRandR and focused-input actions now cross a bounded, unknown-field-rejecting Unix protocol into
  `cloudless-desktop-agent`, which runs inside the `cloudless` X11 session and authenticates the
  exact `cloudlessd` peer UID. Browser opens remain typed atomic request files consumed by the
  existing desktop browser agent. `cloudlessd` receives neither `.Xauthority` nor membership in
  the desktop account's control-broker group. Tailscale grants its operator capability explicitly
  to `cloudlessd`; power, package, network and container authority remain in their narrow brokers.
- Verification evidence for this boundary is reproducible: `go test ./... -count=1`, `go vet
  ./...`, `distro/scripts/test-privilege-boundaries.sh`,
  `distro/scripts/test-service-hardening.sh`, shell syntax validation and `git diff --check` all
  passed on July 31, 2026. Fresh-install, upgrade and rollback qualification on VirtualBox and DGX
  Spark remains release-blocking.
- Every release now emits a deterministic `cloudless.trust-inventory.v1` artifact bound to the full
  source commit and archive-key fingerprint. It inventories the canonical SHA-256 metadata digest
  for every exact reviewed executable recipe and every validated distributed compatibility
  profile. The artifact is detached-signed, hash-bound inside the signed release manifest and
  verified with the packaged offline archive key. A mandatory `trust-inventory` gate rejects a
  missing recipe, invalid distributed profile, nondeterministic inventory or incomplete artifact
  set; the atomic publication test verifies its signature with all other release artifacts.

Done gate:

- Independent security review has no unresolved critical/high finding.
- Credential rotation and recovery are documented and exercised.

## P9 — Release qualification and supportability

- [ ] Build a CI matrix for AMD64/ARM64, unit/integration/API concurrency, frontend syntax and visual
  regression, package installation, upgrade and rollback.
- [x] Maintain a physical test matrix for VirtualBox, generic NVIDIA PCs, one Spark and two-to-eight
  Spark subsets.
- [ ] Add soak tests for repeated model switches, app lifecycle, updates, browser/terminal use and
  cluster churn.
- [x] Publish installation, recovery, update, cluster, terminal, privacy and support-bundle guides.
- [x] Define experimental, preview and supported labels for apps, engines, recipes and hardware
  profiles.
- [x] Establish release candidates, rollback windows, telemetry/privacy policy and a support SLA
  before declaring 1.0.

Implementation status (July 31, 2026):

- The single `CloudlessOS qualification` workflow now runs the full Go suite under the race
  detector, `go vet`, AMD64/ARM64 cross-compilation, browser JavaScript parsing, release/security
  policy tests and the encrypted recovery rehearsal.
- A three-target platform matrix validates generic AMD64, generic ARM64 and DGX Spark ARM64
  behavior. A package job builds both Debian generations and inspects their payload contracts.
- Windows CI captures the responsive visual smoke matrix and retains the screenshots. The lifecycle
  soak repeats job/operation stores plus selected application, inference, recipe Abort, rollback
  and reboot-recovery tests three times on each push and 25 times every Monday. It also repeats
  browser/terminal session contracts, update idempotency, resumable downloads and the complete
  two-to-eight-Spark state/recovery suite. The process-level browser-agent soak now additionally
  proves unavailable-browser retry without request loss, singleton-agent locking, immediate
  graphical-session restart, mode-0660 status publication, unsafe-URL rejection and reuse of the
  same persistent browser profile.
- The package job now installs all six packages, upgrades them and downgrades them to the prior
  generation in isolated Ubuntu AMD64 and emulated ARM64 systems. It verifies every package version,
  executable payload and durable state after both transitions. It now also imports a populated
  legacy model volume twice, verifies setgid/read-only cache ownership and preservation after
  rollback, starts the packaged desktop agent as `cloudless`, proves root is rejected by its
  exact-UID boundary and proves `cloudlessd` is admitted. `package-lifecycle` is a mandatory
  signed-release gate. The exact current AMD64 transition passed locally on July 31, 2026; the
  ARM64 binaries cross-build locally and the existing QEMU-enabled CI job executes the same
  lifecycle because this workstation does not have ARM64 binfmt registered.
- Package qualification now retains the exact AMD64/ARM64 Debian generation plus build, payload,
  upgrade and rollback logs for 30 days even when the job fails. Visual captures and lifecycle-soak
  JSONL/process logs use run-specific artifact names and the same 30-day review window. The source
  policy gate rejects removal of any of these three evidence classes.
- The lifecycle soak now includes a process-level burst of browser requests interrupted by an
  agent restart, proving no request is discarded and the same persistent profile is reused. It
  also drives 64 simultaneous local terminal-proxy sessions alongside 64 rejected remote attempts,
  and churns healthy, failed, reconnected and fully disconnected state across every selected
  cluster size from two through eight for 100 cycles per test invocation. Its API matrix also binds
  seven cluster failure domains to all nine durable non-terminal inference phases and proves the
  UI retains an Abort path without reporting the degraded cluster as ready. Pushes repeat this under
  the race detector three times and the weekly schedule repeats it 25 times. A source-policy check
  prevents these exact contracts from being silently removed from the soak workflow. Every run,
  including a failure, now retains its commit/ref/count metadata, JSONL Go race-test events and
  process-level browser log as a 30-day CI artifact.
- Upgrade/rollback qualification no longer treats retained model bytes as sufficient. The
  disposable package lifecycle migrates a legacy cache before upgrading, runs a deterministic
  OpenAI model endpoint as `cloudlessd` whose readiness depends on reading that exact host-cache
  file, and exposes a running `cloudless-vllm` through the engine protocol. It proves `/api/engine`
  remains active and ready during the candidate installation, after restarting into the candidate,
  during rollback and after restarting into the baseline generation. The legacy source and migrated
  cache are digest-compared after both transitions.
- That package lifecycle now launches the installed `cloudlessd`, desktop agent, browser agent and
  terminal proxy together under their real service identities. Its disposable X11 executor proves
  root peer rejection, daemon-authorized display query, virtual-key delivery, durable display
  rollback after a daemon restart, browser request consumption with the same persistent profile
  and survival of the terminal session across that restart. The rehearsal runs for AMD64 and the
  QEMU-backed ARM64 package generation rather than relying only on source-level mocks.
- Real interactive browser/terminal endurance, live multi-Spark churn and physical hardware
  matrices remain open; therefore the broader soak checklist is not marked complete yet.
- `physical-validation-matrix.json` defines the exact VirtualBox, generic NVIDIA, single-Spark and
  every two-to-eight-Spark target, with common and cluster-specific required checks.
  [`RELEASE_QUALIFICATION.md`](./RELEASE_QUALIFICATION.md) defines the evidence directory, redaction
  rules and artifacts that must be retained; CI rejects an incomplete matrix contract. The
  packaged `cloudless-qualify` runner now validates that the selected target matches the current
  architecture/platform, records unique kernel boot IDs, requires hashed evidence for every
  observed check, automatically expands the required cluster checks for two-to-eight-node targets,
  rejects credential-shaped notes and text/ZIP evidence, collects read-only machine data and the
  redacted support ZIP, and emits a qualification result only when the complete evidence tree
  revalidates. Its `plan` and `next` commands turn the full campaign into a resumable guided
  checklist, identify failed or digest-changed evidence, explain each observation and expose the
  same state as JSON for a future GUI. A changed artifact or matrix invalidates and removes a stale
  result. The updater now
  retains the exact source commit obtained from its signed release manifest, and qualification
  automatically consumes it only when the installed version matches; fresh ISO candidates retain
  an explicit full-commit fallback.
- Completed physical campaigns can now be exported as one private, atomic ZIP. The exporter refuses
  incomplete or mutated campaigns, inventories and hashes every boot/check/evidence member, binds
  them back to the sealed qualification result and verifies the finished archive before publication.
  A separate offline `verify-export` command makes retained VM/Spark evidence independently
  reviewable without trusting the mutable source directory.
- The release-level `verify-set` gate rejects missing or duplicate hardware targets and any mixture
  of versions or source commits. Its private manifest binds the authoritative physical matrix plus
  every archive's SHA-256 and size, so a release decision can prove that VM, generic NVIDIA and all
  required Spark-topology evidence came from one exact candidate.
- Release generation now consumes that set rather than leaving it beside the build as informal
  operator context. Every signed release publishes a detached-signed physical-qualification
  descriptor bound into its gate attestation. Pre-1.0 builds disclose `not-qualified`; a stable
  1.0+ generation fails before building unless all physical targets match its exact version and
  full source commit.
- Retained release manifests and standalone signed artifacts are now isolated by both version and
  channel. A stable promotion can no longer overwrite beta qualification evidence, signatures or
  immutable artifact paths for the same semantic version.
- Stable publication now starts from the public, archive-signed beta generation for the exact full
  source commit. The promotion gate verifies the complete six-package/two-architecture matrix,
  every standalone payload and detached signature, the validation/compatibility/qualification
  bindings and a seven-day soak. Stable repository construction reuses those exact package, SBOM,
  catalog, trust-inventory and installer bytes while regenerating only channel-bound metadata and
  signatures.
- [`OPERATIONS.md`](./OPERATIONS.md) indexes the install, DGX, update, recovery, cluster, terminal,
  privacy, diagnostics/support and qualification guides. Privacy documentation distinguishes local
  inference from explicit update, registry, Hugging Face, Tailscale, Cloudflare and browser traffic,
  and support documentation requires reviewing the locally generated, never-auto-uploaded bundle.
- Apps and managed engines now receive a validated `experimental`, `preview` or `supported` level
  from the signed catalog; locally registered engines always remain experimental. Reviewed,
  package-authenticated recipes are supported, constrained declarative containers are preview and
  editable/unreviewed recipes are experimental. Hardware capabilities use the same vocabulary,
  with unknown future capabilities failing to experimental. The API and App, Engine and Recipe
  surfaces expose these labels, and contract tests prevent an invalid or missing tier.
- [`RELEASE_POLICY.md`](./RELEASE_POLICY.md) requires an immutable-commit beta candidate, a
  seven-day soak, complete retained evidence and repeated stable verification; retains two stable
  rollback generations for at least 90 days; defines pre-1.0 triage targets for each support tier;
  and prohibits new telemetry without explicit opt-in, disclosure and retention controls. It also
  records an operationally monitored security contact as a remaining pre-1.0 gap.

Done gate:

- All required matrices are green for a release candidate and a rollback rehearsal succeeds.
- Known limitations are explicit, discoverable in the UI and documented.

## Recommended execution order

1. Complete P0 recipe/inference reliability.
2. Complete P1 release-pipeline safety and rotate exposed credentials.
3. Qualify P2 installation/boot while P3 visual-regression infrastructure proceeds in parallel.
4. Complete P3 and P4 before expanding the catalog.
5. Qualify P5 on physical multi-Spark hardware.
6. Complete P6 and P8 before enabling any public Hermes gateway.
7. Expand P7 only through lifecycle-qualified catalog entries.
8. Run P9 continuously, then use its full gate for a 1.0 release decision.

## Immediate next engineering session

Resume from two coordinated tracks. Hardware qualification is release-blocking, but it must not
prevent safe engineering work that can be completed and tested locally.

Engineering track:

1. Retain the AMD64 and QEMU/ARM64 CI evidence from the installed-session integration rehearsal;
   the package lifecycle now exercises the daemon, desktop agent, browser and terminal together.
2. Retain the AMD64 and QEMU/ARM64 CI evidence for active-model upgrade/rollback continuity; the
   deterministic package rehearsal now covers the migrated host cache and real readiness contract.
3. Retain the first green `lifecycle-soak` CI artifact from the browser restart, concurrent terminal
   and two-to-eight-Spark churn workflow, then retain a weekly 25-repeat artifact for the release
   candidate.
4. Retain a physical VM/Spark restore rehearsal using the encrypted control-plane backup.

Operator/physical qualification track:

1. Run the new graphical-boot audit through ten clean VirtualBox install/boot/restart cycles and
   retain Settings status plus `cloudless-diagnostics` evidence for every failed or repaired boot.
2. Repeat the graphical qualification on representative NVIDIA hardware and DGX Spark appliance
   mode, verifying the per-boot splash and Spark's reduced-motion default on the real display stack.
3. Exercise update and rollback continuity with a loaded model and an interrupted durable recipe
   preparation on a VM and physical targets; retain the before/after support bundles.
4. Run ten clean install/boot/restart cycles on VirtualBox and representative NVIDIA hardware,
   validating locale, timezone, display scaling, resolution, power controls and virtual keyboard.
5. Run the recipe lifecycle and node-loss matrices on one Spark and a physical multi-Spark cluster,
   exporting a secret-free diagnostic bundle for every failure and recovery case.

Do not start P3 visual redesign work ahead of a P2 failure that can still produce a black screen.
Visual-regression infrastructure may proceed in parallel, but release qualification remains blocked
until the physical P1/P2 evidence above is retained with the release candidate.
