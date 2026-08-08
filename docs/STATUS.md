# CloudlessOS live status

**Last updated:** 2026-08-02

CloudlessOS is in active pre-release development. The orchestrator, fullscreen interface,
installer/update pipeline and DGX Spark layer work on development hardware, but public production
readiness still requires broader hardware, security, recovery and licensing validation.

## Working now

- Go orchestrator and embedded local web interface.
- Ubuntu 24.04 AMD64 appliance ISO build and test pipeline.
- Six independently versioned Debian packages: orchestrator, shell, branding, hardware,
  firstboot and updater.
- Signed APT repository and atomic Cloudflare R2 publication for AMD64 and ARM64.
- DGX Spark installation as a reversible layer over NVIDIA's qualified DGX OS.
- Unified capability system for generic NVIDIA and DGX Spark product differences.
- Managed vLLM, SGLang and llama.cpp engine contracts with a locked private endpoint and a stable,
  user-configurable authenticated client identity.
- Model Manager, Hugging Face account connection, downloads, fit guidance and advanced launch
  arguments.
- Hermes Agent-powered Cloudless Assistant and scoped model/agent API keys.
- Optional application catalog, machine-local native recipes, daemon-owned background execution,
  progress/ETA, abort and persistent app state.
- First launch, welcome tour, themes, scaling/resolution controls and kiosk recovery.
- NVIDIA driver checking on generic systems and DGX Dashboard access on Spark.
- Two-to-eight-Spark discovery, guided connection, validation, telemetry and managed distributed
  vLLM lifecycle; one and two Sparks are supported targets, while three through eight are preview.
- Update UI with progress, changelog, restart state and package rollback support.
- Authenticated draggable/full-screen host terminal with up to eight persistent tabs, proxied
  inside the Cloudless interface.
- Optional `cloudless-developer-tools` compiler toolchain and automatic CUDA login environment.
- First-class local custom-engine registration for source-built vLLM/SGLang images, including
  managed model/cache/gateway lifecycle, readiness checks, unload/abort and rollback.
- Fail-closed inference promotion for bundled engines, custom engines and native recipes: stable
  `/v1/models` protocol/model verification, bounded promotion and cleanup of a failed candidate to
  an explicit unloaded/error state.
- Persistent coordinator and worker recipe lifecycle paths that survive orchestrator restarts.
- Recipe Library separation between executable signed/constrained profiles and non-executable local
  drafts, with exact-model installation required before per-machine validation or launch.
- Cloudless accounts in the top bar, including email/password and Google, X, or GitHub sign-in,
  profile display, and revocable scoped publisher API keys.
- Community Recipe Manager with guided in-Cloudless publishing, an external CLI publishing path,
  immutable revisions, asynchronous policy validation, signed discovery/install, author pages,
  stars, ratings, comments, reports, notifications, and exact-revision revocation checks.
- Cloudless Doctor reconciliation of unloaded state against running recipe containers, including a
  narrowly scoped coordinator/worker orphan-runtime repair that preserves downloaded weights.
- Stable pointer behavior in the kiosk; Ubuntu's idle cursor-hiding service is disabled because it
  produced apparent foreground-window blinking on noisy pointing devices.
- Normalized engine metrics on the authenticated gateway at `/metrics` (Prometheus text) and
  `/v1/metrics` (JSON), re-exported as engine-independent `cloudless_*` series so LAN, Tailnet and
  tunnel clients can read live queue depth, KV-cache pressure and token counters without the
  private engine port being published.

The custom-engine developer path is documented in
[`CUSTOM_ENGINES.md`](./CUSTOM_ENGINES.md).

The current gateway identity and recipe lifecycle are documented in
[`INFERENCE_API.md`](./INFERENCE_API.md) and [`LOCAL_RECIPES.md`](./LOCAL_RECIPES.md).

## Current custom-engine boundaries

- Registration accepts a Docker image already present on the machine.
- Custom images inherit vLLM or SGLang compatibility contracts.
- Images are visibly local/unverified and never pulled or overwritten by Cloudless updates.
- Managed engines remain selectable as recovery.
- Custom builds currently run locally and are not distributed to Spark cluster peers.
- Arbitrary host executables and Python virtual environments cannot be registered directly.

## Current recipe and cluster-transfer boundaries

- Arbitrary local recipe commands are not trusted or executable. The **Runnable** library admits
  exact profiles authenticated by the installed signed Cloudless package and constrained
  declarative containers; editable definitions remain under **Drafts**.
- “Cloudless reviewed” is package provenance, not a background review queue. It means the complete
  executable profile exactly matches one shipped in the authenticated package; an edit removes
  that status.
- Recipe Check/Run requires the exact model revision to be completely installed. **Validate** then
  records compatibility evidence for the current recipe revision and Spark topology before Run is
  enabled.
- Recipe work continues when Model Manager is closed and remains abortable from the desktop.
- A private recipe health check is not activation; the locked stable endpoint must also return the
  required OpenAI response and internal `cloudless` model identity.
- Multi-Spark `buildOnce` and `downloadOnce` copy coordinator artifacts to peers over SSH by
  default. Model weights can instead use the coordinator Spark's automatically managed NFSv4.2
  hub, or an Advanced Interface custom NFSv4.1/4.2 export. The shared hub is bound into the
  standard cache on every worker; runtime images and node-specific CUDA/JIT caches remain local
  and are distributed or prepared normally.
- Coordinator-to-peer transfer uses a per-file content manifest, durable staging and atomic
  promotion. Verified peer files are retained and interrupted transfers resume missing or invalid
  content instead of recopying the complete repository.

## Verification baseline

The current implementation changes have passed:

- `go test ./...`;
- `go vet ./...`;
- production daemon build;
- embedded JavaScript validation;
- ARM64 cross-build;
- physical DGX Spark deploy and `/api/health` check;
- physical local-image registration and removal smoke test;
- inference identity persistence, validation, request/response rewriting and live listener rebind;
- browser smoke test of an API port change, including closure of the old listener.

The July 31 exact-commit local qualification passed source/security checks, generic AMD64 and
ARM64 behavior, DGX Spark ARM64 behavior, dual-architecture package construction, package-content
validation, graphical boot audits and privilege/service hardening. It then stopped in the package
lifecycle group because the browser-agent test timed out waiting for its test URL. The incomplete,
never-signed evidence is retained under `distro/out/qualification/local-runs/.incomplete-*` for
diagnosis. No release should describe that run as green.

An actual SM121 source build and model-serving benchmark is still a hardware qualification task,
not something inferred from registration tests.

## Before a public production release

- Complete the supported AMD64 GPU/driver/Secure Boot installation matrix.
- Repeat clean-install, interrupted-update, rollback and recovery tests.
- Qualify every visible app and engine image on its advertised architectures.
- Validate a pinned SM121 vLLM source build end to end through Model Manager, Hermes and gateway.
- Complete one- and two-Spark fault, disconnect, image/version and long-running workload tests.
- Keep three-to-eight-Spark support at preview until physical systems are available for each claim.
- Resolve the browser-agent lifecycle timeout and retain one complete seven-group local run.
- Replace whole-directory peer cache copying with verified resumable content-addressed transfer.
- Finish threat modeling for terminal, custom images, public API exposure and agent permissions.
- Produce end-user installation, recovery, privacy and support policies.

## Wrap-up and resume point

Development was intentionally paused on July 31, 2026 after consolidating the release path and
documentation. GitHub Actions are disabled; qualification and publishing run locally. The source
supports one signed AMD64/ARM64 pipeline, exact-commit evidence, atomic R2 publication, reversible
updates, VM/generic NVIDIA operation, and the one-/two-Spark product path. Three-to-eight Sparks are
implemented as preview topology support, not a release-blocking or physically validated claim.

Resume in this order:

1. Fix and repeat the browser-agent package lifecycle test until
   `distro/scripts/run-local-qualification.sh` produces a complete retained run.
2. Run and export required campaigns for VirtualBox, generic NVIDIA, one Spark and two Sparks.
3. Rehearse update/rollback with an active model, interrupted preparation, graphical boot,
   display/locale/keyboard/power controls, encrypted restore and the two-Spark failure matrix.
4. Complete the security review, public support/escalation ownership and credential rotation before
   a 1.0 stable release. Rotation is intentionally deferred, not completed.
5. Run the required beta soak and only then make a stable-release decision.

## Active engineering direction

- Reliability and accurate progress/error reporting before catalog breadth.
- One signed cross-architecture pipeline rather than platform branches.
- Managed defaults plus explicit reversible expert workflows.
- Keep distributed advanced-container community recipes inside the enrolled Spark topology,
  immutable artifact, bounded InfiniBand-device, validation, ownership, and rollback contracts.

## Documentation

- [Project README](../README.md)
- [Documentation index](./README.md)
- [Architecture](./ARCHITECTURE.md)
- [Roadmap](./ROADMAP.md)
- [Development environment](./DEV_ENVIRONMENT.md)
- [Custom engines](./CUSTOM_ENGINES.md)
- [Inference API identity](./INFERENCE_API.md)
- [Local inference recipes](./LOCAL_RECIPES.md)
- [Publish and discover community recipes](./COMMUNITY_RECIPES.md)
- [DGX Spark](../distro/DGX-SPARK.md)
- [Decision log](./DECISIONS.md)
