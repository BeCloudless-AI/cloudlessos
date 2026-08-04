# CloudlessOS

**A local-AI operating-system layer built with web technologies on Ubuntu 24.04.**

CloudlessOS turns an NVIDIA-powered computer into a private AI appliance. Its local Go
daemon manages inference engines, models, agentic applications, GPU resources, networking,
updates and the fullscreen browser interface. The same interface works directly on a
CloudlessOS display or from a browser on the machine.

CloudlessOS supports standard AMD64 NVIDIA computers and NVIDIA DGX Spark ARM64 systems.
DGX Spark installations preserve NVIDIA's qualified DGX OS stack and add Cloudless as a
signed, reversible layer.

## Current capabilities

- Guided CloudlessOS installer pipeline for Ubuntu 24.04 AMD64.
- Signed AMD64 and ARM64 Debian-package update channel.
- DGX Spark appliance and side-by-side installation modes.
- One active OpenAI-compatible inference engine with managed vLLM, SGLang and llama.cpp
  choices.
- Model Manager with curated models, Hugging Face account support and fit guidance.
- Cloudless accounts plus a signed community Recipe Manager with guided publishing, discovery,
  immutable versions, author profiles, ratings, comments, and revocation checks.
- Hermes Agent-powered Cloudless Assistant using the selected local model.
- Optional AI applications and background native recipes with progress, abort and multi-Spark
  distribution.
- Two-to-eight-Spark cluster setup, validation, monitoring and distributed inference.
- Local, LAN and user-approved public API access with an editable client port and model alias.
- Authenticated draggable/full-screen host terminal with persistent tabs and an optional
  source-build toolchain.
- Locally compiled vLLM/SGLang images selectable as first-class custom engines.

The project is under active development. Read [Live Status](./docs/STATUS.md) for validated
behavior and current limitations rather than relying on old release assumptions.

## Developer quick start

### Requirements

- Ubuntu 24.04, or Ubuntu 24.04 under WSL2 for interface/orchestrator development.
- Docker and NVIDIA Container Toolkit for GPU containers.
- Go 1.26 or newer.
- Node.js for the embedded-interface syntax check.

### Run the orchestrator from source

```bash
git clone https://github.com/BeCloudless-AI/cloudlessos.git
cd cloudlessos

# Ubuntu/WSL helpers, only when the dependencies are not installed yet
bash scripts/setup-wsl-docker.sh
bash scripts/install-go.sh

cd orchestrator
go run ./cmd/cloudlessd
```

Open [http://127.0.0.1:8765](http://127.0.0.1:8765). The first provisioning run may pull
large engine images and model files.

For UI-only development without automatic container provisioning:

```bash
cd orchestrator
CLOUDLESS_NO_PROVISION=1 go run ./cmd/cloudlessd
```

### Validate a change

```bash
cd orchestrator
go test ./...
go vet ./...
go build ./...
cd ..
node distro/scripts/test-web-js.js
```

The broader distro, package, architecture and release gates are documented in
[Building CloudlessOS](./distro/README.md).

## Compile and use a custom inference engine

Advanced users can compile vLLM or SGLang from source, tag the result as a local Docker
image, and register it from **Settings -> Engine -> Custom engine builds**. The custom
engine then uses Cloudless's Model Manager selection, model cache, API gateway, Hermes
integration, metrics and lifecycle controls. Managed vLLM remains available for rollback.

Start with the complete developer runbook:

**[Build and register a custom inference engine](./docs/CUSTOM_ENGINES.md)**

The short version on an installed CloudlessOS machine is:

```bash
# Open Cloudless Terminal first
sudo cloudless-developer-tools

# Build a trusted, pinned vLLM source revision as a uniquely tagged image.
# Then register that exact image tag in Settings -> Engine.
```

Cloudless registers container images rather than arbitrary host executables. This keeps
experimental CUDA/Python dependencies isolated and makes returning to the signed engine
predictable.

## Build or install CloudlessOS

- [Build the AMD64 installer ISO and Debian packages](./distro/README.md)
- [Install on NVIDIA DGX Spark](./distro/DGX-SPARK.md)
- [Publish signed updates](./distro/UPDATES.md)
- [Platform and architecture capability rules](./distro/CAPABILITIES.md)

The generic installer and the DGX Spark layer share the same orchestrator and interface,
while architecture-specific images, hardware setup and capability gates remain explicit.

## Repository map

| Path | Purpose |
|---|---|
| [`orchestrator/`](./orchestrator) | Go daemon, APIs, engine lifecycle and embedded web interface |
| [`distro/`](./distro) | Debian packages, ISO tooling, DGX Spark installer and release pipeline |
| [`docs/`](./docs) | Architecture, decisions, hardware, developer and product documentation |
| [`scripts/`](./scripts) | Development environment, build and validation helpers |

Important documentation:

- [User guide](./docs/USER_GUIDE.md)
- [Architecture](./docs/ARCHITECTURE.md)
- [Inference API identity](./docs/INFERENCE_API.md)
- [Local inference recipes](./docs/LOCAL_RECIPES.md)
- [Publish and discover community recipes](./docs/COMMUNITY_RECIPES.md)
- [Custom engines](./docs/CUSTOM_ENGINES.md)
- [Development environment](./docs/DEV_ENVIRONMENT.md)
- [Hardware and multi-GPU strategy](./docs/HARDWARE.md)
- [Hermes integration](./docs/HERMES_INTEGRATION.md)
- [Decision log](./docs/DECISIONS.md)
- [Roadmap](./docs/ROADMAP.md)
- [Live status](./docs/STATUS.md)

## Security model for developer features

- The Cloudless interface and terminal proxy listen on loopback by default.
- Terminal starts `/bin/login`; it does not provide anonymous root access.
- The authenticated OS user receives exactly their normal host permissions, including
  `sudo` only when their Linux account is authorized for it.
- A custom engine runs trusted user-supplied code with GPU and model-cache access. Register
  only code and images you trust.
- Custom images are never silently promoted into the signed Cloudless update channel.

## License

License selection is still pending. Track the decision in [DECISIONS.md](./docs/DECISIONS.md).
