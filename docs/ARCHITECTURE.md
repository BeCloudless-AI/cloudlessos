# CloudlessOS architecture

CloudlessOS is a local control plane for private AI workloads. The fullscreen browser is a
thin presentation shell; the Go daemon, signed contracts and host/container runtime own the
system behavior.

```text
Fullscreen Chromium or local browser
                |
                | loopback HTTP / SSE / WebSocket
                v
Embedded Cloudless web interface
                |
                | local control API
                v
cloudlessd orchestrator + OpenAI-compatible gateway
       |                 |                    |
       v                 v                    v
Engine lifecycle   Models and apps     Hardware / OS control
       |                 |                    |
       +---------- Docker + NVIDIA -----------+
                         |
           managed or registered custom engine
                         |
               stable cloudless-ai:8000/v1
                         |
          Hermes, applications and API clients
```

## Presentation and local access

- The installed appliance uses Chromium, Openbox and LightDM to show the local interface.
- The same interface can be opened from a normal browser during development.
- The UI/API listens on `127.0.0.1:8765` by default.
- The key-authenticated model and agent gateway listens on `127.0.0.1:8766`.
- The web terminal is a separate loopback-only ttyd service running `/bin/login`, proxied
  through the Cloudless origin at `/terminal/`.

The terminal requires normal OS authentication. It deliberately provides the authenticated
user's real host permissions rather than a Cloudless-specific restricted shell.

## Orchestrator

`cloudlessd` is responsible for:

- asynchronous install, start, stop, reset, update and uninstall operations;
- inference-engine selection and exactly-one-engine enforcement;
- model selection, downloads, cache reuse and fit guidance;
- the stable OpenAI-compatible endpoint used by Hermes and installed applications;
- API keys and opt-in LAN/public exposure;
- hardware telemetry and DGX Spark cluster management;
- OS update, driver, display, power and diagnostic integrations;
- durable install state under `/var/lib/cloudless`.

Long operations use job IDs and Server-Sent Events rather than blocking requests.

## Inference engine contract

Exactly one engine serves the selected model. Consumers do not connect to a container name;
they use the stable internal alias `cloudless-ai` on port `8000`, or the authenticated
Cloudless gateway on port `8766`.

Every engine contract defines:

- image and architecture/platform availability;
- GPU, IPC, network, port and volume settings;
- model substitution and launch arguments;
- OpenAI-compatible health and metrics expectations;
- memory, disk and accelerator guidance.

Managed vLLM, SGLang and llama.cpp definitions come from the signed embedded catalog.

## Custom inference engines

A registered custom engine is a locally built Docker image adapted to an existing managed
vLLM or SGLang contract. This deliberately reuses the production lifecycle instead of
adding a second custom-process manager.

The registration persists only its generated ID, display name, local image tag, compatibility
base, entrypoint mode and creation time. At activation Cloudless:

1. stops the active engine;
2. applies the base contract with the custom image;
3. substitutes the current Model Manager selection;
4. attaches the shared model cache and private network alias;
5. waits for `/v1/models` before declaring the engine ready.

An upstream vLLM image may already define `vllm serve` as its entrypoint. Registration
inspects the local image and removes the duplicate command prefix when required.

Custom images are never signed, pulled, upgraded or deleted by Cloudless updates. They are
trusted local code with GPU and model-cache access. The managed engine remains available as
rollback. See [CUSTOM_ENGINES.md](./CUSTOM_ENGINES.md).

## Containers and isolation

Docker plus NVIDIA Container Toolkit is the supported runtime. Containers make CUDA/Python
dependencies reproducible, keep applications independently removable, and allow custom
source builds without modifying the signed OS engine environment.

The `cloudless` Docker network is private. Engine containers receive the stable
`cloudless-ai` alias. Host ports are bound to loopback unless a signed exposure contract and
explicit user action permit LAN or public access.

## Models and applications

- Model metadata is curated and architecture-aware; users may also import Hugging Face models.
- The selected model is independent from the selected compatible engine.
- Hermes Agent is a core system integration and follows the stable Cloudless engine endpoint.
- Optional applications are described through signed catalog contracts and installed on demand.
- Local native recipes remain machine-owned and separate from the custom generic-engine path.

## Hardware and DGX Spark

Hardware detection reports GPU/accelerator, unified memory, storage, driver and platform
capabilities. Standard AMD64 and DGX Spark ARM64 use the same interface and orchestrator,
with architecture-specific images and signed capability gates.

DGX Spark is installed as a reversible package layer over NVIDIA's qualified DGX OS. Managed
vLLM can use the Spark cluster lifecycle for two to eight connected systems. Custom engines
currently run on one machine; Cloudless does not distribute an arbitrary local image to peers.

## Packaging and updates

CloudlessOS uses Ubuntu 24.04 and separately versioned Debian packages for the orchestrator,
shell, branding, hardware setup, first boot and updater. AMD64 installations use the guided
installer ISO. DGX Spark uses the signed ARM64 installer layer.

Updates come from the signed APT repository at `updates.becloudless.ai`. The pipeline validates
both architectures as one release generation. Platform-specific behavior uses server-side
capabilities, not a forked frontend.

## Security invariants

- Control surfaces default to loopback.
- Public and LAN exposure is explicit and contract-gated.
- API secrets are not sent to inference containers.
- Only one inference engine owns the stable alias at a time.
- Managed artifacts remain pinned and signed.
- Custom engine images are visibly unverified and never enter the signed update channel.
- Removing a custom registration does not delete user source or image data.
- Terminal is authenticated; it is not anonymous root access.

## See also

- [Custom engine developer guide](./CUSTOM_ENGINES.md)
- [Hardware strategy](./HARDWARE.md)
- [Capability and platform locks](../distro/CAPABILITIES.md)
- [DGX Spark installation and operations](../distro/DGX-SPARK.md)
- [Decision log](./DECISIONS.md)
- [Live status](./STATUS.md)
