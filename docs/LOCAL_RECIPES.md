# Local inference recipes

Cloudless recipes are machine-owned, editable launch profiles for specialized inference setups.
They use the native `cloudless.recipe/v1` format and run through Cloudless itself; SparkRun or
another third-party recipe runtime is not installed or required.

Recipes are available from **Model Manager -> Recipes**. A user can create one locally or import
a reviewed manifest, check it without changing the active model, and then launch it through the
same Cloudless model lifecycle used by the desktop.

## What a recipe controls

A recipe can declare:

- a pinned source and revision;
- a model repository and revision;
- a reviewed preparation command and runtime command;
- required tools and environment variables;
- one or more Spark nodes and distributed-runtime settings;
- health checks and the private OpenAI-compatible API path;
- whether runtime preparation and model download happen once on the coordinator.

Recipe commands are powerful local code. Review their source, revision, image and commands before
running them. Saving a recipe does not make it Cloudless-verified or add it to signed updates.

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
other Spark systems over the configured private SSH link. The transfer is a tar stream over SSH;
the high-speed cable provides the network path, but end-to-end speed also depends on source reads,
tar processing, SSH encryption and destination writes.

The current model-cache check requires both:

- the exact requested Hugging Face snapshot directory on the peer; and
- the same total byte size for the repository cache directory on coordinator and peer.

If a prior transfer was interrupted, a snapshot is incomplete, or unrelated cache metadata makes
the directory sizes differ, Cloudless currently removes that peer repository directory and copies
the complete repository again. The peer transfer is not yet file-level incremental or resumable.
This is why a model may be copied again even though part of it already exists on the second Spark.
The progress display should identify the peer and transferred bytes while this is happening.

Do not manually modify a peer's cache during an active launch. Resumable, content-addressed peer
distribution is tracked as hardening work in [ROADMAP.md](./ROADMAP.md).

## Check a recipe first

The **Check** action validates cluster size, topology, required tools and pinned source without
downloading the model or replacing the active engine. A passing check means that the declared
requirements are present; it is not a guarantee that an unverified upstream runtime will start or
produce correct inference.

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

### The private health check passes but promotion fails

Read the final launch error. Confirm the configured `proxyHost`, private port and `/v1/models`
response. A host-networked server that binds only to `127.0.0.1` is not reachable through the
stable proxy; configure its server host as `0.0.0.0`. Do not expose port `8000` publicly—the
authenticated Cloudless gateway remains the client entry point.

### A recipe is running but API clients cannot find its native model name

Clients must use the model alias shown in **Settings -> API access**, not the recipe's upstream
model ID. The gateway translates that identity to the active private runtime.
