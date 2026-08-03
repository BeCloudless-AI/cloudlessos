# Local inference recipes

> Recipe execution is currently experimental. The ordered hardening work and release gates are
> tracked in the [recipe reliability roadmap](./RECIPE_RELIABILITY_ROADMAP.md).

Cloudless recipes are portable launch profiles for specialized inference setups.
They use the native `cloudless.recipe/v1` format and run through Cloudless itself; SparkRun or
another third-party recipe runtime is not installed or required.

Recipes are available from **Model Manager -> Recipes**. The page separates **Runnable** profiles
from **Drafts**. Runnable profiles are admitted by the installed Cloudless package or the
constrained declarative container policy. The interface manages recipes but does not author them:
advanced users write and validate the native YAML/JSON manifest with the Cloudless CLI, then
publish it to the shared Recipe Manager. Imported legacy drafts can be inspected or removed, but
arbitrary host-command drafts cannot cross into Validate or Run.

To author and publish a constrained recipe to the shared Recipe Manager, follow
[`COMMUNITY_RECIPES.md`](./COMMUNITY_RECIPES.md). It documents the CLI/API-key workflow used both
inside and outside CloudlessOS.

“Cloudless reviewed” is deliberately literal and offline: the complete executable definition was
inspected and tested by the Cloudless project, compiled into a Cloudless package, and authenticated
by the archive signature installed on the machine. It is not an automatic scanner, a pending review
request or a remote moderation service. Changing any executable field makes that saved definition a
local draft immediately.

## What a recipe controls

A recipe can declare:

- a pinned source and revision;
- a model repository and revision;
- a reviewed preparation command and runtime command;
- required tools and environment variables;
- one or more Spark nodes and distributed-runtime settings;
- health checks and the private OpenAI-compatible API path;
- whether runtime preparation and model download happen once on the coordinator.

Recipe commands are powerful local code. Cloudless displays their source, revision, image,
commands and requested permissions, but it does not execute editable or unreviewed host-command
recipes. The current `source-scripts-v1` adapter is executable only when the complete profile
exactly matches one authenticated inside the signed CloudlessOS package. Any edit removes that
status and blocks both Check and Run before a process, image build or Docker operation begins.

The `managed-container-v1` runner remains the command-free path for standard user-authored vLLM
containers. `advanced-container-v1` supports immutable custom engine images that own their complete
in-container command, architecture-specific environment, auxiliary pinned models, writable-root
mode, IPC, ulimits, declared Linux capabilities, and bounded NCCL launch across enrolled DGX
Sparks. Distributed advanced containers receive per-node rank and fabric identity from Cloudless,
while Cloudless copies the pinned image and verified model cache before launch. Both adapters deny host commands, arbitrary
host mounts, and the Docker socket and bind only the private inference port. Recipes that require
host source scripts still need an exact signed compatibility profile. A passing validation never
changes local code into a reviewed profile.

## Install, validate and run

A recipe being visible does not imply its weights are installed. The normal sequence is:

1. Under **Runnable**, choose **Install model** when the exact model revision is absent. Cloudless
   opens that model in Model Manager.
2. After the download is complete, choose **Validate**. This checks the exact recipe revision,
   runtime, architecture, capacity, ports, fabric and selected Spark topology without replacing the
   active model.
3. Choose **Run recipe** only after the current validation succeeds.

Cloudless rejects direct Check/Run API requests when the exact model snapshot is incomplete. An
`.incomplete` snapshot never counts as installed. Validation evidence is revision- and
topology-specific; editing the recipe or changing selected Sparks requires validation again.

## Launch lifecycle

Recipe launches are background jobs. Closing Model Manager or using another Cloudless application
does not cancel the operation. The desktop shows the active operation with its current phase,
byte progress when available, percentage, elapsed time and estimated remaining time. Reopening
Recipes reconnects to the same job.

A typical distributed launch performs these phases:

1. validate the pinned recipe and cluster topology;
2. reuse or prepare the runtime on the coordinator;
3. distribute missing runtime data to peer Sparks;
4. reuse or download model weights on the coordinator;
5. distribute missing model data to peer Sparks;
6. start the runtime on every required node;
7. wait for the OpenAI-compatible health check;
8. create the stable Cloudless route;
9. verify the full stable endpoint contract;
10. persist the recipe as active.

**Abort** signals the active job and its child processes. Completed model weights and runtime
artifacts remain cached so a later launch can reuse them. **Stop** unloads a running recipe and
clears the active inference route without deleting the saved recipe or caches.

Reviewed source checkouts and generated stop metadata live under
`/var/lib/cloudless/recipes-runtime`. This persistent location allows Cloudless to stop or replace
a recipe after an orchestrator update or restart; temporary directories must never own runtime
lifecycle state.

Distributed worker checkouts use
`/home/<enrolled-user>/.local/share/cloudless/recipes-runtime/<recipe-id>`. This location is both
persistent and writable by the enrolled user used for peer SSH operations. Neither coordinator nor
worker lifecycle data may live in `/tmp` or `/var/tmp`; the orchestrator's systemd service uses a
private temporary namespace that is intentionally discarded when the daemon restarts.

## Promotion guardrails

A successful recipe health check does **not** make the recipe active. The private runtime may be
healthy while still being unreachable from Cloudless—for example, when a host-networked server
binds only to `127.0.0.1` and the stable proxy must reach it through another address.

Before committing active state, Cloudless probes the permanent internal endpoint at
`http://127.0.0.1:8000/v1/models`. Promotion succeeds only when the endpoint:

- accepts a connection within the bounded readiness period;
- returns HTTP `200`;
- returns valid OpenAI-compatible models JSON; and
- includes the required internal model ID `cloudless`.

If any check fails, Cloudless fails closed. It removes the stable proxy, runs the recipe's reviewed
stop lifecycle, clears active-recipe state, marks inference as unloaded, and reports a terminal,
actionable error. It does not leave the desktop indefinitely displaying **Cloudless AI is starting
up**. For a host-networked recipe, the inference server normally must bind to `0.0.0.0`; the stable
port remains loopback-only and is not thereby exposed to the LAN.

Stopping a recipe follows the same ownership rule. Cloudless does not clear active state if it
cannot prepare or run the persisted stop lifecycle; the stop job fails visibly so an operator can
inspect the runtime instead of being told it was unloaded when it may still own GPU memory or a
port.

Automated regression tests enforce the required model identity, the reviewed DGX Spark host-bind
rewrite, rejection of an unknown bind layout, persistent coordinator storage, persistent
user-writable worker storage and worker-username validation. Release verification must keep these
tests passing for both supported architectures.

## Stable API behavior

Recipes cannot choose the client-facing API port or public model name. Their backend listens on a
private recipe port (the native default is `8890`) and uses the internal model identity
`cloudless`. Cloudless then exposes it through the API identity configured in
**Settings -> API access**. See [INFERENCE_API.md](./INFERENCE_API.md).

This prevents a new or imported recipe from silently breaking Hermes, installed applications, or
an existing LAN API integration.

## Cache and multi-Spark transfer behavior

With `downloadOnce` or `buildOnce`, the coordinator prepares an artifact once and copies it to the
other selected Spark systems over the configured private SSH link. Cloudless inventories each file
by path, type, size and SHA-256, keeps verified peer files, and retransmits only missing or invalid
content. Interrupted transfers resume from durable staging and are promoted only after the complete
snapshot matches the coordinator's cryptographic manifest.

Model downloads also use a stable model-and-revision staging volume. Hugging Face partial chunks
survive navigation, abort and daemon restart. A completed download is copied into a promotion tree,
verified again, and atomically exchanged with the active cache; the last complete cache is retained
until promotion succeeds.

Cloudless periodically garbage-collects old recipe-owned artifacts. Saved recipes, active or
recovering operations, unresolved cleanup obligations, Hugging Face refs, live containers and
`.incomplete` chunks are protected. Eligible old checkouts, staging trees/volumes, unreferenced model
revisions and orphaned custom images are removed oldest-first. Under storage pressure the retention
window becomes shorter, but the ownership protections remain unchanged. Read-only status and a
manual cleanup pass are available through `GET /api/recipes/cache` and
`POST /api/recipes/cache/cleanup`.

## Validate a recipe first

The **Validate** action checks cluster size, topology, required tools, pinned source, immutable
runtime image, storage, accelerator compatibility, ports, private fabric and stable API contract
without replacing the active engine. It is available only for an executable recipe with its exact
model revision installed. Passing validation means this signed/constrained profile is launchable on
the current checked topology; it does not convert a Draft into executable code.

### A recipe says Draft only

Open the **Drafts** collection to edit or remove it. Cloudless will not execute arbitrary saved
shell or Docker commands. To become a signed compatibility profile, its exact definition must be
reviewed, tested, added to Cloudless source and delivered in an authenticated Cloudless package.
There is no automatic background review process.

### A previously reviewed recipe became a draft after an update

Do not bypass the trust check. Confirm that the saved definition was not edited. Cloudless contains
targeted migrations for exact known legacy built-in fields, such as the old MiaAI model-cache
location; all other differences stay drafts intentionally. Remove and re-import the packaged
profile if the local edits are not required.

## Troubleshooting

### A launch appears after Model Manager was closed

This is expected. Recipe work belongs to the daemon, not to the modal. Use the desktop operation
card or reopen **Model Manager -> Recipes** to inspect or abort it.

### A peer starts copying the model again

The peer failed the exact snapshot-and-directory-size cache check described above. This commonly
follows an aborted or failed transfer. Let the current copy complete, or abort it knowing that the
next launch may need to replace the partial peer cache again.

### The runtime exits with status 1

Open the recipe failure details and inspect the reported command and stderr. Confirm the pinned
revision, image architecture, model revision, cluster size and health path. A recipe that needs a
different CLI or API shape requires a dedicated reviewed adapter; changing the public Cloudless
port or alias is not a compatible fix.

### Cloudless is unloaded but accelerator memory is still occupied

Open **Settings > Cloudless Doctor**. If a saved recipe's container is still running, Doctor shows
**Orphaned inference runtime** and offers **Stop orphaned runtime**. This constrained repair removes
only exact matched containers locally and on selected workers and preserves downloaded model files.
If the repair still needs attention, save the operation details/support bundle: an unreachable
worker or an unmatched process must be resolved before Cloudless can claim cleanup succeeded.

### The private health check passes but promotion fails

Read the final launch error. Confirm the configured `proxyHost`, private port and `/v1/models`
response. A host-networked server that binds only to `127.0.0.1` is not reachable through the
stable proxy; configure its server host as `0.0.0.0`. Do not expose port `8000` publicly—the
authenticated Cloudless gateway remains the client entry point.

### A recipe is running but API clients cannot find its native model name

Clients must use the model alias shown in **Settings -> API access**, not the recipe's upstream
model ID. The gateway translates that identity to the active private runtime.
