# cloudlessd — CloudlessOS orchestrator

`cloudlessd` is the local Go daemon behind CloudlessOS. It manages inference engines,
models, AI applications, Spark clusters, system services and the embedded web interface.

## Layout

```text
cmd/cloudlessd/       daemon entrypoint
cmd/cloudless-updater/ signed package updater
internal/api/         local API, gateway and embedded interface
internal/engine/      Docker runtime abstraction
internal/catalog/     signed, architecture-aware app and engine contracts
internal/customengine/ locally-built engine registration adapter
internal/provision/   startup and engine lifecycle
internal/models/      curated model metadata
internal/localrecipes/ machine-local native recipes
internal/sparkcluster/ DGX Spark discovery, setup and distributed inference
internal/assistant/   Hermes-backed Cloudless Assistant integration
internal/state/       install-wide persisted state and registrations
internal/hardware/    GPU and system telemetry
internal/jobs/        asynchronous operations and SSE progress
```

## Run from source

From Ubuntu or WSL2:

```bash
cd orchestrator
go run ./cmd/cloudlessd
```

Open `http://127.0.0.1:8765`.

Requirements are Docker, NVIDIA Container Toolkit for GPU use, and Go 1.26 or newer.
`CLOUDLESS_NO_PROVISION=1` disables automatic app/engine provisioning for UI-focused
development.

The primary listeners are:

- `127.0.0.1:8765`: CloudlessOS UI and local control API.
- `127.0.0.1:8766`: key-authenticated OpenAI-compatible model and agent gateway.
- `127.0.0.1:7681`: authenticated ttyd terminal, proxied through `/terminal/`.

Production values are configured in `distro/packages/cloudless-orchestrator/cloudless.env`.

## Validate changes

```bash
cd orchestrator
go test ./...
go vet ./...
go build ./...
cd ..
node distro/scripts/test-web-js.js
```

Package, architecture, ISO and release gates live under `distro/scripts/` and are explained
in [`../distro/README.md`](../distro/README.md).

## Engine lifecycle

Exactly one inference engine owns the stable internal endpoint
`http://cloudless-ai:8000/v1`. Managed vLLM, SGLang and llama.cpp engines and registered
custom builds all use the same switch, load, readiness, metrics, unload and abort paths.
The Cloudless gateway and Hermes therefore do not need to change endpoints when the engine
changes.

The active model comes from Model Manager. vLLM and SGLang receive Hugging Face model IDs;
llama.cpp follows its GGUF command contract. DGX Spark may use the distributed managed-vLLM
path when a healthy cluster is configured.

## Custom source-built engines

Advanced users can compile vLLM or SGLang source into a local Docker image and register it
in **Settings -> Engine -> Custom engine builds**. The custom image inherits one managed
base contract, including GPU flags, model-cache volumes, the selected model, private
networking and `/v1/models` health validation.

Custom images are not part of the signed catalog, are never automatically pulled or updated,
and currently run on one machine rather than being copied to Spark peers.

The complete developer workflow is in
[`../docs/CUSTOM_ENGINES.md`](../docs/CUSTOM_ENGINES.md).

## Selected API routes

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/health` | daemon and Docker health |
| GET | `/api/catalog` | supported app catalog |
| GET | `/api/apps` | managed container state |
| GET | `/api/models` | model catalog and local state |
| GET | `/api/engine` | selected/active engines, readiness and operation |
| POST | `/api/engine/{id}` | activate a managed or custom engine |
| POST | `/api/engine/load` | load the selected engine/model |
| POST | `/api/engine/unload` | release inference memory |
| POST | `/api/engine/abort` | cancel an engine launch |
| POST | `/api/engines/custom` | register a local vLLM/SGLang image |
| DELETE | `/api/engines/custom/{id}` | remove an inactive registration |
| GET | `/api/engine/metrics` | normalized engine metrics |
| GET | `/api/gateway` | API keys and gateway exposure state |
| POST | `/api/assistant/chat` | streamed Cloudless Assistant response |
| GET | `/api/jobs/{id}` | asynchronous job snapshot |
| GET | `/api/jobs/{id}/events` | SSE job progress |
| GET | `/api/system/spark-cluster` | DGX Spark cluster state |

The browser UI is the primary client. API behavior must remain loopback-safe, asynchronous
for long operations, and consistent across managed and custom engines.

## State and update boundaries

Production state is stored under `/var/lib/cloudless`. Custom engine registrations persist
there but reference user-owned Docker images. Signed Cloudless package updates can update
the orchestration contract without overwriting a custom image or source checkout.

Image and model pins are architecture-aware. Do not add browser-only platform locks; use the
capability contract documented in [`../distro/CAPABILITIES.md`](../distro/CAPABILITIES.md).
