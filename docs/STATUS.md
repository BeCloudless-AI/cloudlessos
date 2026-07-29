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
- Managed vLLM, SGLang and llama.cpp engine contracts with one stable API endpoint.
- Model Manager, Hugging Face account connection, downloads, fit guidance and advanced launch
  arguments.
- Hermes Agent-powered Cloudless Assistant and scoped model/agent API keys.
- Optional application catalog, machine-local native recipes and persistent app state.
- First launch, welcome tour, themes, scaling/resolution controls and kiosk recovery.
- NVIDIA driver checking on generic systems and DGX Dashboard access on Spark.
- Two-to-eight-Spark discovery, guided connection, validation, telemetry and managed distributed
  vLLM lifecycle.
- Update UI with progress, changelog, restart state and package rollback support.
- Authenticated full host terminal proxied inside the Cloudless interface.
- Optional `cloudless-developer-tools` compiler toolchain and automatic CUDA login environment.
- First-class local custom-engine registration for source-built vLLM/SGLang images, including
  managed model/cache/gateway lifecycle, readiness checks, unload/abort and rollback.

The custom-engine developer path is documented in
[`CUSTOM_ENGINES.md`](./CUSTOM_ENGINES.md).

## Current custom-engine boundaries

- Registration accepts a Docker image already present on the machine.
- Custom images inherit vLLM or SGLang compatibility contracts.
- Images are visibly local/unverified and never pulled or overwritten by Cloudless updates.
- Managed engines remain selectable as recovery.
- Custom builds currently run locally and are not distributed to Spark cluster peers.
- Arbitrary host executables and Python virtual environments cannot be registered directly.

## Verification baseline

The current custom-engine implementation has passed:

- `go test ./...`;
- `go vet ./...`;
- production daemon build;
- embedded JavaScript validation;
- ARM64 cross-build;
- physical DGX Spark deploy and `/api/health` check;
- physical local-image registration and removal smoke test.

An actual SM121 source build and model-serving benchmark is still a hardware qualification task,
not something inferred from registration tests.

## Before a public production release

- Complete the supported AMD64 GPU/driver/Secure Boot installation matrix.
- Repeat clean-install, interrupted-update, rollback and recovery tests.
- Qualify every visible app and engine image on its advertised architectures.
- Validate a pinned SM121 vLLM source build end to end through Model Manager, Hermes and gateway.
- Complete multi-Spark fault, disconnect, image/version and long-running workload tests.
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
- [DGX Spark](../distro/DGX-SPARK.md)
- [Decision log](./DECISIONS.md)
