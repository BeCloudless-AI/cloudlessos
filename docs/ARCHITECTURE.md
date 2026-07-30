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
cloudlessd orchestrator + authenticated OpenAI-compatible gateway
       |                 |                    |
       v                 v                    v
Engine lifecycle   Models and apps     Hardware / OS control
       |                 |                    |
       +---------- Docker + NVIDIA -----------+
                         |
           managed or registered custom engine
                         |
         locked private cloudless-ai:8000/v1
                         |
              Hermes and managed services

API clients -> configurable authenticated gateway (default :8766/v1)
```

## Presentation and local access

- The installed appliance uses Chromium, Openbox and LightDM to show the local interface.
- The same interface can be opened from a normal browser during development.
- The UI/API listens on `127.0.0.1:8765` by default.
- The key-authenticated model and agent gateway listens on `127.0.0.1:8766` by default. Its
  client-facing port and model alias are editable in **Settings -> API access**.
- The web terminal is a separate loopback-only ttyd service running `/bin/login`, proxied
  through the Cloudless origin at `/terminal/`.

The terminal requires normal OS authentication. It deliberately provides the authenticated
user's real host permissions rather than a Cloudless-specific restricted shell. Its floating
window supports up to eight persistent terminal tabs; hiding the window does not recreate a tab.

The Browser is another static desktop tool, but it is deliberately not an iframe or an arbitrary
HTTP reverse proxy. `cloudlessd` validates an HTTP(S) target and writes a request into
`/run/cloudless-browser/requests`; the unprivileged graphical-session agent opens it in a separate
persistent Chromium profile. Native tabs, site isolation, downloads, history and password storage
therefore remain browser-owned, while the Cloudless kiosk stays available behind the window.

Tailscale is the optional private remote-access plane. The official Linux client is installed by a
separate systemd oneshot after boot so package upgrades never nest APT inside dpkg. It starts logged
out. Cloudless Settings can request interactive login, Tailscale Serve for the local dashboard, and
Tailscale SSH, but Tailscale remains the authority for identity, encryption and tailnet policy.

## Orchestrator

`cloudlessd` is responsible for:

- asynchronous install, start, stop, reset, update and uninstall operations;
- inference-engine selection and exactly-one-engine enforcement;
- model selection, downloads, cache reuse and fit guidance;
- the locked private OpenAI-compatible endpoint used by Hermes and installed applications;
- the configurable authenticated API identity used by local, LAN and public clients;
- API keys and opt-in LAN/public exposure;
- hardware telemetry and DGX Spark cluster management;
- OS update, driver, display, power and diagnostic integrations;
- durable install state under `/var/lib/cloudless`.

Long operations use daemon-owned job IDs rather than blocking requests. App installs, resets and
uninstalls continue after the initiating HTTP request or browser view disappears. The interface
reconstructs their current state from `GET /api/jobs` and may use Server-Sent Events for low-latency
updates, but an SSE connection never owns the work.

Operations lock the smallest safe resource set. Two unrelated apps may install or uninstall in
parallel, while operations that touch the same app or a shared dependency serialize in a stable
sorted order. This prevents both same-app corruption and pack dependency deadlocks. Job snapshots
include overall percentage, phase-local items or bytes, elapsed time and an estimated remaining
time so a slow download remains distinguishable from a stalled launch.

The left-rail **Update Center** is the unified presentation surface for these authorities. It
aggregates the signed CloudlessOS package status, platform-owned NVIDIA/DGX maintenance and live
registry comparisons for installed applications and built-in inference engines. It does not
replace their enforcement paths: system packages still run through the privileged signed updater,
DGX OS remains NVIDIA-owned, and
application updates use the same daemon-owned per-app jobs as installation. Replacement images are
downloaded before the running container is removed, keeping the current app available until the
brief cutover.

Managed vLLM, SGLang and llama.cpp updates use a dedicated background job. An inactive engine is
only prefetched. If the active engine is running its built-in base image, Cloudless downloads the
reviewed image first and then recreates the engine with the same model, execution mode and stable
API identity; Spark workers receive that same image. Model-specific runtime images and registered
custom engines are never redirected or replaced by this update path.

## Inference engine contract

Exactly one engine or native recipe serves the selected model. Managed consumers use the stable
internal alias `cloudless-ai` on port `8000` with the model name `cloudless`. API clients use the
authenticated Cloudless gateway, whose defaults are port `8766` and model alias `cloudless`.
The client port and alias may change, but the private port and identity cannot.

The gateway rewrites client model requests to the private identity and presents the configured
alias in OpenAI-compatible JSON and streaming responses. Port changes rebind the listener live and
recreate enabled LAN or tunnel exposure. See [INFERENCE_API.md](./INFERENCE_API.md).

Every engine contract defines:

- image and architecture/platform availability;
- GPU, IPC, network, port and volume settings;
- model substitution and launch arguments;
- OpenAI-compatible health and metrics expectations;
- memory, disk and accelerator guidance.

Managed vLLM, SGLang and llama.cpp definitions come from the signed embedded catalog.

### Fail-closed runtime promotion

Cloudless separates runtime health from route promotion. A container may be running, or a native
recipe's private health URL may pass, without satisfying the endpoint used by Hermes and other
Cloudless consumers. The activation sequence is therefore:

1. start the private runtime;
2. pass its engine- or recipe-specific health check;
3. create the stable route;
4. require `GET /v1/models` on loopback port `8000` to return HTTP `200`, valid OpenAI JSON and
   model ID `cloudless`;
5. only then persist the runtime as active and loaded.

Failure of the stable-contract check in step 4 removes the stable proxy and candidate runtime,
stops any distributed worker, and persists an unloaded state. Managed and custom engine launches
also use deferred cleanup for earlier activation failures. Startup contexts are bounded, so the UI
receives a terminal job error rather than inferring readiness from a surviving process or
displaying an unbounded loading state.

Native recipe lifecycle assets are also part of this reliability boundary. Coordinator checkouts
and stop metadata live under `/var/lib/cloudless/recipes-runtime`; worker assets live in the
enrolled user's `~/.local/share/cloudless/recipes-runtime`. They are deliberately outside temporary
directories because systemd private temporary namespaces do not survive daemon restarts.

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

Launch-time sanitization preserves the private contract even when an advanced user saved custom
arguments: an engine cannot override Cloudless's port `8000` or served model name `cloudless`.

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
  They run as daemon-owned background jobs with check, progress, abort, cache reuse and optional
  coordinator-to-peer distribution. See [LOCAL_RECIPES.md](./LOCAL_RECIPES.md).

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
- Recipes and custom engines cannot override the client-facing API identity or private runtime
  contract.
- Runtime activation is committed only after the stable endpoint proves reachability, protocol
  compatibility and the required internal model identity.
- Failed stable-contract promotion cleans up the candidate runtime and records an explicit
  unloaded/error state.
- Managed artifacts remain pinned and signed.
- Custom engine images are visibly unverified and never enter the signed update channel.
- Removing a custom registration does not delete user source or image data.
- Terminal is authenticated; it is not anonymous root access.

## See also

- [Custom engine developer guide](./CUSTOM_ENGINES.md)
- [Inference API identity](./INFERENCE_API.md)
- [Local inference recipes](./LOCAL_RECIPES.md)
- [Hardware strategy](./HARDWARE.md)
- [Capability and platform locks](../distro/CAPABILITIES.md)
- [DGX Spark installation and operations](../distro/DGX-SPARK.md)
- [Decision log](./DECISIONS.md)
- [Live status](./STATUS.md)
