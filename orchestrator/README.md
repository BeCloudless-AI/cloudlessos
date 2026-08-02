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
- `127.0.0.1:8766`: default key-authenticated OpenAI-compatible model and agent gateway.
  Its client port and model alias are editable in **Settings -> API access**.
- `127.0.0.1:7681`: authenticated ttyd terminal, proxied through `/terminal/`.
- `/api/system/browser`: validates and queues HTTP(S) addresses for the unprivileged persistent
  Cloudless Browser profile in the active graphical session.
- `/api/system/tailscale/*`: reports Tailscale state and exposes explicit install, login, logout,
  private Serve and SSH actions. Cloudless never receives tailnet credentials.

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

The private runtime identity is deliberately different from the client identity. Engines always
serve `cloudless` at `cloudless-ai:8000`; the authenticated gateway translates the configurable
client model alias and can rebind its listener without restarting `cloudlessd`. See
[`../docs/INFERENCE_API.md`](../docs/INFERENCE_API.md).

Activation is transactional and fail-closed. Before a bundled or custom engine is reported ready,
the stable loopback endpoint must return HTTP `200`, valid OpenAI models JSON and model ID
`cloudless`. A failed or timed-out activation removes the candidate container and proxy, stops any
distributed worker and persists inference as unloaded instead of leaving stale startup state.

## Native recipe lifecycle

Machine-local `cloudless.recipe/v1` profiles run as asynchronous daemon jobs rather than modal-
owned browser work. They support dry checks, progress and ETA, abort, cache reuse, private runtime
routing, and coordinator-to-peer distribution for Spark clusters. Recipes cannot override the
private or client-facing inference identity. Current cache and transfer semantics are documented
in [`../docs/LOCAL_RECIPES.md`](../docs/LOCAL_RECIPES.md).

Execution is admitted independently from editing. An exact `source-scripts-v1` profile must match
one authenticated in the installed package. `managed-container-v1` provides the constrained,
command-free policy; `advanced-container-v1` admits a signed immutable image with its declared
in-container command, auxiliary models, environment, and permissions. Neither container adapter
may invoke host commands or mount arbitrary host paths. Other definitions appear as Drafts and
cannot execute. Check and Run also reject an incomplete/missing exact model snapshot. The UI calls the
per-machine Check operation **Validate** to distinguish compatibility evidence from package
provenance.

Recipe-private health is followed by the same stable `/v1/models` promotion check used for other
engines. Active state is committed only after that check succeeds. Persistent lifecycle data lives
under `/var/lib/cloudless/recipes-runtime` on the coordinator and the enrolled user's
`~/.local/share/cloudless/recipes-runtime` on workers so stop and replacement remain possible after
an orchestrator restart.

Community publication is a separate control plane over the same manifest. The browser uses the
loopback account/community proxy, while `/usr/bin/cloudless recipes validate|publish|status`
supports external author workflows with a scoped publisher API key. Both paths create immutable
revisions in the becloudless.ai service; signed exact releases are verified again before local
installation. See [`../docs/COMMUNITY_RECIPES.md`](../docs/COMMUNITY_RECIPES.md).

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
| POST | `/api/account/signup` | create a becloudless.ai account without exposing credentials to browser cross-origin requests |
| POST | `/api/account/login` | authenticate with email and password through the loopback account gateway |
| POST | `/api/account/refresh` | rotate an authenticated Cloudless account session |
| GET | `/api/account/me` | fetch the signed-in profile |
| POST | `/api/account/logout` | revoke the current account session |
| GET | `/api/account/oauth/{provider}` | begin Google, X or GitHub sign-in with the local Cloudless callback |
| POST | `/api/account/picture` | upload the signed-in user's profile picture |
| GET/POST/DELETE | `/api/account/publisher-keys` | manage scoped external recipe-publishing credentials |
| GET/POST/PUT/PATCH/DELETE | `/api/community/recipes/{rest...}` | proxy account-authorized community recipe and social operations |
| GET | `/api/updates` | unified CloudlessOS, platform-driver, managed-engine and installed-app update inventory |
| POST | `/api/updates/check` | start system/driver checks before refreshing registry comparisons |
| POST | `/api/updates/apps/apply` | start selected application updates as independent background jobs |
| POST | `/api/updates/engines/{id}/apply` | update a built-in inference engine in the background; custom engines are excluded |
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
| POST | `/api/settings/inference-contract` | change the client API port and model alias |
| GET | `/api/recipes` | local recipes, active jobs and inference contract |
| POST | `/api/recipes/{id}/check` | validate an executable, installed recipe on the current topology |
| POST | `/api/recipes/{id}/run` | launch a recipe as a background job |
| POST | `/api/recipes/{id}/stop` | stop an active or exact detected orphaned recipe runtime |
| POST | `/api/recipes/{id}/abort` | abort an active recipe operation |
| GET | `/api/system/doctor` | read-only system, cluster, promotion and orphan-runtime reconciliation |
| GET | `/api/system/doctor/bundle` | create a redacted local support ZIP |
| POST | `/api/assistant/chat` | streamed Cloudless Assistant response |
| GET | `/api/jobs` | reconnectable snapshots for all asynchronous jobs (optional `prefix`) |
| GET | `/api/jobs/{id}` | asynchronous job snapshot |
| GET | `/api/jobs/{id}/events` | SSE job progress |
| GET | `/api/system/spark-cluster` | DGX Spark cluster state |

The browser UI is the primary client. API behavior must remain loopback-safe, asynchronous
for long operations, and consistent across managed and custom engines.

Cloudless account traffic is proxied by `cloudlessd` to `https://becloudless.ai/api` so the
kiosk never needs permissive cross-origin access. Override the upstream only for development with
`CLOUDLESS_ACCOUNT_API`. OAuth providers must allow the exact redirect URL
`http://127.0.0.1:8765/auth/callback` in the Supabase Authentication URL configuration.

## State and update boundaries

Production state is stored under `/var/lib/cloudless`. Custom engine registrations persist
there but reference user-owned Docker images. Signed Cloudless package updates can update
the orchestration contract without overwriting a custom image or source checkout.

Image and model pins are architecture-aware. Do not add browser-only platform locks; use the
capability contract documented in [`../distro/CAPABILITIES.md`](../distro/CAPABILITIES.md).
