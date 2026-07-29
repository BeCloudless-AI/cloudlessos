# CloudlessOS project context

Read this file first, then [`docs/STATUS.md`](./docs/STATUS.md) for current validated state and
[`docs/ARCHITECTURE.md`](./docs/ARCHITECTURE.md) for the current system design. Historical choices
and superseded approaches remain in [`docs/DECISIONS.md`](./docs/DECISIONS.md).

## Product

CloudlessOS is a local-AI operating-system layer built on Ubuntu 24.04. A Go daemon manages
inference engines, models, applications, Hermes Agent, GPU resources, networking, updates and a
fullscreen web interface. Standard AMD64 NVIDIA systems and NVIDIA DGX Spark ARM64 systems share
one source tree and signed release pipeline.

The kiosk browser is a thin shell. Product authority belongs to backend capabilities, signed
catalog contracts and the orchestrator—not browser-only state.

## Current invariants

- Docker and NVIDIA Container Toolkit provide the supported GPU application runtime.
- Exactly one inference engine owns `cloudless-ai:8000/v1` at a time.
- Hermes, applications and API clients follow that stable endpoint.
- Managed vLLM is the default; SGLang and llama.cpp are alternative contracts.
- DGX Spark preserves NVIDIA's qualified DGX OS and uses signed ARM64 Cloudless packages.
- Platform differences use backend capability gates, not frontend forks.
- Production updates come from the signed repository at `updates.becloudless.ai`.
- The terminal is loopback-only, authenticated and launches a normal `/bin/login`.
- Custom vLLM/SGLang source builds are registered as local Docker images, inherit a managed
  engine contract, and never enter the signed update channel.

## Development

The primary checkout is `D:\Cloudless`; WSL maps it at `/mnt/d/Cloudless`. Go builds on the
Windows filesystem use `GOFLAGS=-buildvcs=false`. Use repository scripts for complex WSL commands
rather than deeply nested PowerShell/Bash quoting.

Core validation:

```bash
cd orchestrator
go test ./...
go vet ./...
go build ./...
cd ..
node distro/scripts/test-web-js.js
```

Do not commit model weights, caches, custom images, credentials or generated release output.

## Documentation map

- [`README.md`](./README.md): project and developer entry point.
- [`docs/README.md`](./docs/README.md): documentation index.
- [`docs/CUSTOM_ENGINES.md`](./docs/CUSTOM_ENGINES.md): source-build and registration runbook.
- [`distro/README.md`](./distro/README.md): packages and ISO.
- [`distro/DGX-SPARK.md`](./distro/DGX-SPARK.md): DGX Spark installation and operation.
- [`distro/UPDATES.md`](./distro/UPDATES.md): signing and publication.

## Working agreement

- Preserve unrelated work in a dirty tree.
- Use `apply_patch` for source edits.
- Keep Status, Architecture and developer guides consistent with implementation.
- Record meaningful technical policy in Decisions.
- Dates are absolute `YYYY-MM-DD`.
- Never embed credentials in source, documentation or committed environment files.
