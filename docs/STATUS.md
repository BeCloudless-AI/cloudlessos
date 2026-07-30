# CloudlessOS live status

**Last updated:** 2026-07-29

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
  vLLM lifecycle.
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

- Local recipes are trusted machine-owned code, not signed community content.
- Recipe work continues when Model Manager is closed and remains abortable from the desktop.
- A private recipe health check is not activation; the locked stable endpoint must also return the
  required OpenAI response and internal `cloudless` model identity.
- Multi-Spark `buildOnce` and `downloadOnce` copy coordinator artifacts to peers over SSH.
- Peer model validation currently compares an exact snapshot and whole repository directory size.
  Partial or mismatched caches are replaced with a complete tar-stream copy; transfer is not yet
  incremental or resumable.

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

An actual SM121 source build and model-serving benchmark is still a hardware qualification task,
not something inferred from registration tests.

## Before a public production release

- Complete the supported AMD64 GPU/driver/Secure Boot installation matrix.
- Repeat clean-install, interrupted-update, rollback and recovery tests.
- Qualify every visible app and engine image on its advertised architectures.
- Validate a pinned SM121 vLLM source build end to end through Model Manager, Hermes and gateway.
- Complete multi-Spark fault, disconnect, image/version and long-running workload tests.
- Replace whole-directory peer cache copying with verified resumable content-addressed transfer.
- Finish threat modeling for terminal, custom images, public API exposure and agent permissions.
- Decide and publish the project license.
- Produce end-user installation, recovery, privacy and support policies.

## Active engineering direction

- Reliability and accurate progress/error reporting before catalog breadth.
- One signed cross-architecture pipeline rather than platform branches.
- Managed defaults plus explicit reversible expert workflows.
- Native Cloudless community recipes later, with identity, provenance, validation and moderation.

## Documentation

- [Project README](../README.md)
- [Documentation index](./README.md)
- [Architecture](./ARCHITECTURE.md)
- [Roadmap](./ROADMAP.md)
- [Development environment](./DEV_ENVIRONMENT.md)
- [Custom engines](./CUSTOM_ENGINES.md)
- [Inference API identity](./INFERENCE_API.md)
- [Local inference recipes](./LOCAL_RECIPES.md)
- [DGX Spark](../distro/DGX-SPARK.md)
- [Decision log](./DECISIONS.md)
