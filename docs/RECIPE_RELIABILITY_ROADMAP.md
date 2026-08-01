# Recipe reliability roadmap

This document turns the July 2026 recipe lifecycle audit into an implementation plan. It is the
source of truth for hardening recipe checks, preparation, launch, recovery and cleanup. Work should
be completed in phase order unless an earlier phase explicitly allows parallel delivery.

## How to resume this roadmap

This section is the handoff checkpoint for a future Codex task. Read it before editing lifecycle
code, then update it whenever a roadmap item changes state.

Status meanings:

- **Implemented** means the code and focused automated tests exist.
- **Verified** means the relevant full test suite passes in the repository.
- **Qualified** means the release-gate scenario has passed on the target architecture and, where
  applicable, physical DGX Spark hardware.
- A checked roadmap item means implemented and verified. It does **not** imply hardware
  qualification unless its exit gate explicitly says so.

Current checkpoint (July 31, 2026):

- Phases 0 through 4 are substantially implemented. Their physical hardware and fault-injection
  release gates still need to be run as a complete matrix.
- Phase 5 has durable disconnect protection, journal-driven peer cleanup, coordinated boot,
  topology-generation locking, exact worker-subset placement and user-driven degraded cleanup.
  Its automated suite passes; the physical multi-Spark release matrix is not yet qualified.
- Phase 6 and the remaining Phase 0 ownership/inventory work are implemented and verified in the
  automated suite. The next engineering task is to close the deliberately unchecked Phase 1–2
  items (custom-build image preflight, NCCL/contract probes, tombstoning and full restart recovery),
  then execute the physical
  multi-Spark release matrix. Do not start additional inference-engine features before these gates.
- The working tree may contain the implementation described here without a release commit. Inspect
  `git status`, run the full Go suite, and preserve unrelated user changes before continuing.
- The Recipe Library now defaults to executable **Runnable** profiles and keeps arbitrary local
  definitions under **Drafts**. Exact model installation is an API-level prerequisite for Validate
  and Run, not merely a disabled browser button. “Reviewed” means an exact package-authenticated
  compatibility profile; it never means queued or automatic remote review.
- Cloudless Doctor now reconciles unloaded inference state with observed recipe containers. Its
  repair is intentionally narrower than recipe execution: it can repair legacy runtime-directory
  ownership and remove only exact observed container names on the coordinator and selected workers,
  while preserving model caches and refusing arbitrary Docker arguments.

Required verification after every lifecycle change:

```text
cd /mnt/d/Cloudless/orchestrator
/usr/local/go/bin/gofmt -w <changed Go files>
/usr/local/go/bin/go test ./...
```

Then run `git diff --check` from the repository root. Never publish recipe reliability changes based
only on focused tests.

## Problem groups this roadmap must eliminate

Every task below must trace back to at least one of these observed failure classes:

1. A recipe passes Check but fails at launch because the image, command, port, architecture,
   topology or model contract differs at runtime.
2. Preparation restarts, redownloads or recopies large model artifacts after navigation, abort,
   daemon restart or role reversal.
3. A failed launch leaves Cloudless indefinitely in “AI is starting,” retains ports or processes,
   or loses the previously working engine.
4. Abort acknowledges the click but does not prove that local and peer resources stopped.
5. Cluster disconnect, node loss or reboot strands a distributed process on another Spark.
6. The UI exposes generic `exit status 1`, blinking state, inaccurate percentages or no useful
   explanation of which node and phase are blocking progress.
7. Custom recipes bypass the stable Cloudless API port or expose a model identity other than
   `cloudless`.

## Target reliability contract

A recipe launch must behave as a durable transaction:

1. **Preflight** proves that the requested source, image, nodes, storage, ports and runtime contract
   are usable without replacing the active model.
2. **Prepare** creates verified, resumable artifacts in staging locations while the current model
   remains available.
3. **Promote** replaces the active engine only after the prepared runtime is ready, then commits all
   active state atomically.
4. **Rollback or reconcile** returns Cloudless to a truthful, usable state after any error, abort,
   daemon restart, node restart or cluster topology change.

The UI must never report a recipe as ready when its stable API is unavailable, and no failed or
aborted operation may silently retain a port, container, model process or accelerator allocation.

## Phase 0 — Baseline and invariants

Goal: make the expected behavior executable before changing the lifecycle.

- [x] **RR-001 — Lifecycle state model.** Define durable states for `checking`, `preparing`,
  `prepared`, `switching`, `starting`, `verifying`, `active`, `stopping`, `failed`, `aborted` and
  `recovering`, including valid transitions.
- [x] **RR-002 — Runtime ownership identity.** Give every operation, container, checkout, staging
  directory and peer process an immutable operation ID and recipe revision ID.
- [x] **RR-003 — Failure-injection harness.** Add controllable failures around source checkout,
  image pull, download, transfer, start, health, proxy creation, promotion and state persistence.
- [x] **RR-004 — Resource inventory.** Implement a read-only inventory of recipe-owned containers,
  processes, ports, checkouts, images, model caches and peer artifacts.
- [x] **RR-005 — Preserve stable API invariants.** Encode that only one engine owns the stable API,
  the internal served model is `cloudless`, and clients never connect directly to recipe ports.

Exit gate:

- Lifecycle transition tests exist and reject invalid transitions.
- Every recipe-created resource can be attributed to an operation and recipe revision.
- The current successful launch path remains covered by an end-to-end test.

Implementation status (July 30, 2026):

- RR-001 is implemented in `orchestrator/internal/recipeops`. Run, Check and Stop now create durable
  operation records and move them through validated transitions independently of in-memory UI jobs.
- RR-002 assigns cryptographically random IDs, executable recipe profiles
  receive stable SHA-256 revision identities, and known checkout/image/model/port/proxy resources
  are attributed to the launch operation. The operation identity and a deterministic Compose project
  name are persisted in the local runtime environment copied to peers; Stop reconstructs the original
  active run identity after a daemon restart. Lifecycle containers, direct Docker helpers, native
  process groups, SSH commands and remote rsync processes all receive the same operation/revision
  identity. Checkouts, staging volumes, model caches, immutable images, ports and proxy containers
  are claimed before use, including their last-known peer locator.
- RR-003 is implemented as an internal test-only boundary injector. Source checkout, runtime/image
  preparation, peer and model transfer, model download, engine stop, runtime start, private health,
  proxy pull/create, stable promotion and state commit can each fail at an exact occurrence. There is
  intentionally no production setting or HTTP action that enables these failures. The boundary
  registry is exhaustive and classifies every edge as preserving the current engine or requiring
  rollback; an unclassified production boundary now fails closed. End-state tests cover source
  checkout, image preparation and model-download admission, including durable failure state,
  unchanged active runtime, verified claim cleanup, and retained reusable source/image artifacts.
  A hermetic lifecycle matrix also drives the real run coordinator through runtime-start, private
  health, proxy-pull, proxy-create, stable-promotion and state-commit failures. Every destructive
  case proves the exact previous selection is restored, the candidate runtime and ports are absent,
  cleanup claims are released, and verified image/model artifacts remain reusable.
- RR-004 provides a read-only inventory endpoint at
  `GET /api/recipes/{id}/inventory`. It inspects journaled local checkouts, images, containers,
  Compose projects, native process sets, ports and model caches without mutating them. Persisted
  peer locators use the existing restricted SSH identity to inspect peer checkouts, images, Compose
  projects, labeled containers, processes, ports and staging volumes. Exact peer model-cache revision
  presence remains explicitly `unknown` in this read-only view when only the volume can be proven;
  the destructive-switch gate separately performs full per-file content attestation.
- Check workspaces are now operation-scoped resources in the same inventory and are removed after
  validation. Check no longer clones over the persistent checkout used by an active runtime.
- RR-005 is enforced by the recipe normalizer, inference-contract validation, stable promotion
  probe and serialized engine replacement. Recipes cannot choose the public API port or native
  client-visible model identity; promotion requires the internal `cloudless` alias.
- The focused lifecycle/store tests and the complete `go test ./...` orchestrator suite pass.

## Phase 1 — Honest and comprehensive Check

Goal: a passing Check means the recipe is launchable on the current system, without downloading
full weights or replacing the active engine.

- [x] **RR-101 — Source preflight.** Resolve the revision to an immutable commit, fetch the source
  into an isolated temporary checkout, verify reviewed checksums and apply compatibility transforms.
- [x] **RR-102 — Runtime rendering.** Render the exact environment, compose configuration and
  commands that launch will use; reject unknown or unsafe transformations.
- [x] **RR-103 — Image preflight.** Resolve mutable tags to digests, verify an ARM64/AMD64 manifest,
  inspect entrypoint and required runtime metadata, and require the same digest on every node.
- [x] **RR-104 — Capacity preflight.** Calculate model, image, staging and safety-margin storage for
  each node. Report required and available bytes before preparation begins.
- [x] **RR-105 — Port preflight.** Check the private engine port, distributed rendezvous port and
  stable proxy reservation on every affected node. Identify the owning process/container on conflict.
- [x] **RR-106 — Accelerator preflight.** Verify driver, container runtime, compute capability,
  unified-memory requirements and requested node count.
- [x] **RR-107 — Fabric smoke test.** Run a bounded peer SSH, transfer and distributed bootstrap/NCCL
  test before model preparation. Recheck immediately before switching engines.
- [x] **RR-108 — Contract probe.** Launch a lightweight image-level probe where supported to verify
  the command, bind address, health route and OpenAI-compatible response shape.
- [x] **RR-109 — Check result artifact.** Persist a signed/hash-bound preflight result tied to the
  recipe revision, image digest, platform fingerprint and cluster generation. Invalidate it when any
  of those inputs changes.

Implementation status (July 30, 2026):

- RR-101 uses the same source preparation function for Check and Run. Check clones into a temporary
  directory owned by its durable operation, resolves `HEAD` to a full immutable Git object ID,
  persists that binding, verifies every reviewed SHA-256, applies only the exact reviewed
  compatibility transformations and removes the temporary checkout afterward. Regression tests
  prove that Check cannot alias the active runtime checkout and that changed reviewed files fail.
- RR-102 renders `.env.dspark`, the operation identity, deterministic Compose project name, worker
  paths and runtime working directory exactly as Run does. It verifies every lifecycle executable,
  rejects relative scripts that escape the rendered working directory, requires referenced scripts
  to exist and validates the managed Compose file with `docker compose config --quiet` without
  launching containers.
- RR-103 resolves registry tags through Docker Buildx to an immutable Linux manifest for the
  current architecture, inspects image configuration and layer sizes, and prepares that exact
  image on every selected node. Check now builds reviewed custom runtimes too, binds the resulting
  immutable Docker configuration ID, and Run reuses that same output rather than rebuilding it.
  Node attestation repeats before the destructive engine switch.
- RR-104 calculates missing model bytes, expanded runtime-image storage, rendered workspace bytes
  and a minimum five-GiB/ten-percent reserve. It reads free bytes locally and over the enrolled SSH
  identity for every selected peer, then fails before preparation when any node is short.
- RR-105 inspects private engine, rendezvous and stable API listeners on every affected node. It
  permits ports durably owned by the active revision and reports the process identity for conflicts;
  it also rejects disagreement between the stable-port listener and Cloudless engine state.
- RR-106 verifies platform and architecture, NVIDIA driver agreement, compute capability, minimum
  per-node accelerator memory, DGX unified memory, Docker NVIDIA/CDI configuration and requested
  node count.
- RR-107 has bounded upload/download integrity and peer-to-coordinator TCP bootstrap probes, with
  measured rates saved as evidence. It also launches an actual multi-rank PyTorch/NCCL all-reduce
  inside the exact prepared image across the selected Sparks. The complete fabric probe is repeated
  immediately before the destructive engine switch.
- RR-108 statically verifies the rendered start command and Compose contract use the Cloudless-owned
  private port, non-loopback bind, `/v1` API path and permanent `cloudless` model alias. It then
  starts a short-lived operation-owned container from the exact prepared image and validates strict
  health and OpenAI-compatible `/v1/models` response parsing. The same contract probe repeats
  immediately before the engine switch; full private and stable endpoint probes still gate promotion.
- RR-109 has a durable evidence bundle containing every structured Check result. It is hash-bound to
  the executable recipe revision, resolved source commit, image digest, platform fingerprint and
  cluster-generation fingerprint. The recipe UI distinguishes fully verified evidence, warnings and
  an unchecked revision. Run admission now atomically requires and inherits the newest launchable
  artifact for the exact recipe revision. Immediately before the destructive switch, Run repeats
  source, image, platform, cluster, port and fabric validation and rejects stale evidence.
- A passing Check is now reusable authorization for the exact recipe revision, prepared image,
  selected cluster and Cloudless API contract.

Exit gate:

- Check fails before downloading weights for wrong architecture, insufficient disk, occupied ports,
  missing peer access, incompatible images and invalid runtime commands.
- The UI distinguishes `requirements checked` from `fully launchable` and shows every tested item.
- Check temporary files are removed automatically.

## Phase 2 — Durable jobs, locking and mutation safety

Goal: operations survive service restarts and cannot conflict with recipe mutations or one another.

- [x] **RR-201 — Persistent operation journal.** Store operation ID, recipe snapshot, phase,
  checkpoints, artifact locations, progress, last error and cleanup obligations on disk.
- [x] **RR-202 — Atomic operation admission.** Replace list-then-create checks with one atomic
  `CreateUnique`/compare-and-set operation covering run, check, stop, edit, import and delete.
- [x] **RR-203 — Immutable execution snapshot.** A run uses a stored immutable recipe revision;
  editing creates a new revision and cannot alter the active operation or its stop lifecycle.
- [x] **RR-204 — Mutation guards.** Block delete/re-import/edit while any operation references that
  recipe revision. Deletion becomes tombstoning until owned resources are reconciled.
- [x] **RR-205 — Restart recovery.** On daemon startup, inspect durable operations and runtime
  resources, then resume safe preparation phases or roll back unsafe switching/launch phases.
- [x] **RR-206 — Job retention.** Replace unbounded in-memory job history with bounded durable
  history and numeric/time ordering.
- [x] **RR-207 — Stable progress model.** Track weighted phase progress separately from byte progress;
  keep overall percentage monotonic and calculate ETA from the active phase.

Implementation status (July 31, 2026):

- RR-201 persists the immutable recipe snapshot, exact revision, lifecycle phase, checkpoints,
  structured checks, bound preflight, resource ownership/cleanup classification, monotonic progress,
  ETA and last error with atomic file replacement.
- RR-202 atomically admits run/check/stop operations in the journal, including concurrent callers.
  Edit, re-import and delete execute while holding that same admission lock, closing the cross-store
  check-then-mutate race.
- RR-203 launch and recovery goroutines use the detached recipe snapshot stored in their operation.
  Returned and caller-owned nested maps cannot mutate the persisted snapshot, and mutation admission
  prevents an active revision from being changed underneath its stop lifecycle.
- RR-204 serializes a deletion tombstone with operation admission. The recipe immediately disappears
  from executable lookups and stale browser requests cannot begin new work, while its exact snapshot
  remains available to cleanup, inventory and diagnostics. The management UI shows removal pending,
  unresolved node cleanup can be retried, and the definition is physically purged only after the
  operation journal proves every cleanup-owned resource absent. Tombstones survive restart and are
  finalized automatically when cleanup completes.
- RR-205 marks interrupted work `recovering` before new API admission. Check and pre-switch
  preparation resume from the same immutable snapshot, operation ID, verified model cache and
  staging data after operation-labelled helper resources are reconciled. Switching, starting and
  verifying phases execute compensating cleanup and restore the durable previous runtime where
  possible, otherwise leaving that exact selection explicitly unloaded. A second restart while the
  operation is already recovering reconstructs its original phase from durable checkpoints and
  repeats the same policy. Active runtimes are preserved only when durable state and the stable API
  agree; unresolved local or peer ownership remains blocked and visible.
- RR-206 keeps at most 200 reconciled operation-history entries for 90 days while preserving active
  recovery work and the newest reusable preflight per revision. Ephemeral jobs use numeric creation
  order and retain only their newest 200 terminal entries.
- RR-207 mirrors SSE progress into the durable operation with monotonic overall percentage, separate
  phase/byte progress, elapsed time and ETA. The desktop reconstructs it after an in-memory job loss.

Exit gate:

- Killing `cloudlessd` during every lifecycle phase produces a deterministic resume or rollback.
- Concurrent API tests cannot start two recipe operations or mutate an in-use revision.
- Reopening the UI after a restart reconnects to the durable operation and truthful progress.

## Phase 3 — Verified, resumable artifact preparation

Goal: downloads and peer transfers are resumable, content-verified and never destroy a good cache.

- [x] **RR-301 — Content manifest.** Record cryptographic hashes, sizes and revisions for source,
  model and runtime artifacts. Stop treating equal paths and byte counts as proof of equality.
- [x] **RR-302 — Staged model download.** Download into an operation staging area, preserve valid
  partial chunks, verify the full manifest, then atomically promote the completed cache.
- [x] **RR-303 — Staged peer transfer.** Copy missing content into a peer staging area, verify it,
  and atomically switch the peer cache. Never delete the last complete cache before verification.
- [x] **RR-304 — Resumable transport.** Use chunked/content-addressed transfer with per-file or
  per-chunk checkpoints and bounded retries instead of a single destructive tar stream.
- [x] **RR-305 — Symmetric completion metadata.** Install verified completion manifests on every
  node so coordinator/worker role changes do not trigger unnecessary downloads.
- [x] **RR-306 — First-failure cancellation.** Run producer and consumer waits concurrently; when one
  fails, cancel and reap the other immediately rather than waiting for the global timeout.
- [x] **RR-307 — External dependency policy.** Classify Hugging Face/API failures as retryable,
  authentication, rate-limit, not-found or integrity failures and surface the correct remedy.
- [x] **RR-308 — Cache garbage collection.** Add quota-aware, reference-safe cleanup for obsolete
  staging areas, checkouts, model revisions and images. Never remove active or rollback artifacts.

Implementation status:

- RR-301 now records a deterministic per-file SHA-256 model manifest and verifies it against the
  live snapshot before a cache is considered ready. Repository-escaping symlinks and non-regular
  files are rejected; same-size corruption is covered by a regression test. Source and runtime
  digests remain bound through the Check artifact.
- RR-302 downloads into a deterministic model/revision-specific Docker volume. Hugging Face partial
  chunks survive aborts and daemon restarts, completed data is verified before promotion, and a
  second resumable rsync staging tree protects the active shared cache during the atomic exchange.
- RR-303 now receives peer content under a model-specific staging tree, hashes every staged file,
  and only then promotes it. The previous complete cache is moved to a rollback path and restored
  if promotion fails.
- RR-305 copies the exact verified completion manifest to every peer after activation. A Spark that
  later becomes coordinator can prove it has the same model content without downloading it again.
- RR-306 waits for both sides of a transfer concurrently. The first failing side cancels and reaps
  its counterpart immediately; a regression test covers a failed consumer with an infinite producer.
- RR-304 inventories physical cache files by path, type, size and SHA-256. Retries keep verified
  peer files, remove only missing or invalid staging entries, and transfer that subset. Each peer
  transfer is bounded to three attempts, with durable staging acting as its per-file checkpoint.
- RR-307 classifies authentication, gated-model, rate-limit, missing revision, storage, integrity and
  network failures into actionable remedies, including whether preserved data makes retry safe.
- RR-308 inventories reference ownership before deleting anything. It protects every saved recipe,
  active/recovering operation and unresolved cleanup obligation; preserves Hugging Face refs and
  incomplete downloads; reclaims old workspaces, promotion trees, staging volumes, unreferenced
  model snapshots/blobs and orphaned custom runtime images. Normal and storage-pressure retention
  windows are separate, and Docker refuses image/volume removal while a live consumer remains.

Exit gate:

- Abort/retry transfers only missing or invalid content.
- Same-size corruption is detected.
- Disk exhaustion cannot destroy an existing usable cache.
- Reversing Spark roles reuses verified artifacts.

## Phase 4 — Transactional launch, promotion and rollback

Goal: runtime replacement is all-or-nothing from the user’s perspective.

- [x] **RR-401 — Pre-switch revalidation.** Immediately before stopping the current model, recheck
  cluster generation, peer reachability, storage, ports, image digests and prepared manifests.
- [x] **RR-402 — Named runtime resources.** Launch every helper and inference container with
  operation labels/names or cidfiles so abort and reconciliation can explicitly stop it.
- [x] **RR-403 — Strict private health.** Accept only the configured success codes and response
  contract; never treat redirects, authentication failures or arbitrary 4xx responses as ready.
- [x] **RR-404 — Universal failure cleanup.** Use one deferred cleanup transaction covering start,
  private health, proxy pull, proxy creation, stable promotion, cancellation and timeout failures.
- [x] **RR-405 — Atomic active-state commit.** Persist model, engine, execution mode, recipe revision,
  runtime ownership and unloaded state in one atomic operation after stable promotion succeeds.
- [x] **RR-406 — Previous-engine rollback.** Preserve the prior engine specification until promotion
  succeeds. If the recipe fails after switching, restore the previous healthy engine where possible.
- [x] **RR-407 — Abort guarantee.** Abort transitions to `stopping`, kills named helper/runtime
  containers and peer processes, releases ports/accelerators, verifies absence, then reports terminal
  `aborted`. An abort acknowledgement alone is not completion.
- [x] **RR-408 — Output safety.** Replace scanner-only command handling with bounded streaming that
  cannot deadlock on a very long line; retain bounded structured logs for diagnostics.

Implementation status (July 30, 2026):

- RR-401 now repeats source, image, accelerator, cluster-generation, port, bounded fabric and full
  SHA-256 model checks on every Spark after preparation and before stopping the current model.
- RR-402 uses deterministic Compose project names, injects operation/revision labels through the
  reviewed Docker command path, propagates the identity to child processes and journals local and
  peer container/process/port claims. Recovery addresses resources by those durable identities.
- RR-403 follows no redirects, accepts only direct 2xx health responses, then calls the private
  OpenAI `/v1/models` endpoint and requires the stable `cloudless` model identity before promotion.
- RR-404 routes every failure after engine stop through one compensating cleanup path that removes
  the stable proxy, invokes the reviewed Stop lifecycle and reconciles durable resource ownership.
  Its failure matrix executes the real lifecycle commands, private OpenAI checks, proxy orchestration,
  promotion contract and resource inventory rather than testing only the cleanup helper in isolation.
- RR-405 commits engine, model, execution mode, recipe ownership and unloaded state with one locked,
  atomic state-file replacement; a deterministic persistence-failure test proves a failed save
  restores both the in-memory and on-disk previous runtime.
- RR-406 captures the previous runtime in the run journal at admission. Any failure after switching
  cleans the candidate, restores the previous state and restarts its managed engine; if restart itself
  fails, the exact previous selection is retained but explicitly marked unloaded. Persisting that
  safe fallback is now part of the returned rollback result rather than a silently ignored write.
- RR-407 persists `stopping` before cancellation and returns the job and operation IDs to the UI.
  Completion invokes the reviewed Stop command, removes operation-labelled containers and process
  groups, inventories every cleanup claim, and reports `aborted` only when absence is verified.
  The cancel handle is bound to the exact durable operation ID, preventing a stale UI/job handle
  from cancelling another run. Tests prove process-tree termination, release of runtime/port claims,
  removal of every operation-labelled GPU container, retention of reusable image/model artifacts,
  and independent cleanup attempts on every peer. An unreachable peer claim remains blocked until a
  later inventory positively proves the remote resource absent.
- RR-408 drains arbitrarily long command lines while retaining a bounded 4-KiB preview, eliminating
  the Scanner token-limit/pipe deadlock. Durable bounded structured command-log history remains.

Exit gate:

- Injected failure at every line of the switch/start/promote sequence leaves either the previous
  engine healthy or Cloudless explicitly unloaded with zero recipe-owned runtime resources.
- No state file can contain a partially promoted recipe.
- Abort completion is proven by resource inventory, not inferred from process termination.

## Phase 5 — Cluster-safe lifecycle and recovery

Goal: topology changes and reboots cannot strand distributed runtimes.

- [x] **RR-501 — Stop before disconnect.** Refuse cluster disconnect while a recipe operation is
  preparing/switching, or explicitly abort and verify cleanup before removing the link.
- [x] **RR-502 — Last-known stop plan.** Persist node addresses, credentials references, runtime
  paths and container identities needed for best-effort cleanup even if current topology is degraded.
- [x] **RR-503 — Partial-node cleanup.** Stop the coordinator locally and attempt every peer
  independently; report unreachable nodes without falsely clearing their cleanup obligations.
- [x] **RR-504 — Coordinated boot.** Prevent worker/coordinator containers from independently
  auto-starting into a broken distributed runtime. Reconcile topology and start them in order.
- [x] **RR-505 — Cluster generation lock.** Bind operations to a topology generation and invalidate
  them when members, addresses, interfaces or link state change.
- [x] **RR-506 — Node subset selection.** Allow an exact compatible subset for a recipe instead of
  making a two-node recipe unusable when three or more Sparks are connected.
- [x] **RR-507 — Degraded recovery UI.** Show which nodes still own resources, offer safe retry of
  cleanup, and prevent a new conflicting launch until ownership is resolved.

Implementation status:

- RR-501 validates every administrator credential before touching a running model, refuses topology
  mutation while a recipe operation or unresolved peer cleanup is in flight, synchronously stops and
  verifies active recipe/managed distributed inference, and only then removes the private fabric.
- RR-502 journals every peer name, durable SSH alias, checkout, Compose project, operation label,
  process set and port before preparation. Recovery uses that snapshot instead of current topology.
- RR-503 attempts journal-driven cleanup on every peer concurrently with independent timeouts. An
  unreachable peer remains an explicit ownership obligation and continues blocking conflicting work.
- RR-504 normalizes every recipe to `restart: no`. On boot, cloudlessd checks exact node count and
  health, runs the reviewed coordinator/worker start sequence, verifies the private contract and only
  then recreates the stable proxy; a failed restart enters normal cleanup instead of a boot loop.
- RR-505 binds Check evidence to a deterministic cluster fingerprint containing membership, host,
  SSH identity, addresses, links and topology creation generation, then repeats it before switching.
- RR-506 stores optional worker fingerprints in the recipe. Check, preparation, pre-switch,
  launch and boot recovery all resolve the same exact subset; automatic placement is deterministic
  and uses only healthy workers. The editor can select a compatible subset from a larger cluster.
- RR-507 keeps unresolved cleanup claims visible by Spark and resource count. Run, mutation and
  removal remain blocked while those claims exist. A durable retry action executes the journaled
  last-known stop plan on every reachable node and releases claims only after inventory proves
  absence.

Exit gate:

- Disconnect, reboot or loss of any node during every phase has a tested outcome.
- Cloudless never claims a cluster recipe is stopped while a reachable node still runs it.
- A two-node recipe can select two nodes from a larger healthy cluster.

## Phase 6 — Security, provenance and operational UX

Goal: make advanced recipes powerful without presenting unverified execution as trusted or opaque.

- [x] **RR-601 — Immutable reviewed provenance.** Reviewed recipes require immutable source commits,
  immutable image digests and signed Cloudless compatibility metadata.
- [x] **RR-602 — Node-consistency attestation.** Before launch, compare source, image, model and
  generated-runtime digests across all selected nodes.
- [x] **RR-603 — Trust presentation.** Clearly distinguish local/unreviewed recipes from signed,
  reviewed recipes; show exactly what commands, images and host permissions will be used.
- [x] **RR-604 — Diagnostic bundle.** Export the operation journal, redacted commands/environment,
  resource inventory, per-node logs, digests, topology and failure classification.
- [x] **RR-605 — Actionable failure UI.** Replace generic `exit status 1` toasts with the failed phase,
  node, command category, retained artifacts, cleanup result and recommended next action.
- [x] **RR-606 — Accurate background UX.** Keep preparation independent from Model Manager; show the
  same operation on the desktop, survive navigation/reload, and expose safe abort/details controls.

Implementation status (July 31, 2026):

- RR-601 uses exact-profile provenance. A `reviewed-import` string is not trusted by
  itself: the complete executable draft must byte-for-byte match a compatibility profile compiled
  into the Cloudless orchestrator package, whose archive key fingerprint and metadata digest are
  then exposed. Registry recipes are rewritten to their checked manifest digest before preparation;
  custom-build recipes bind the produced image ID before model download or engine switch. That
  write-once identity is persisted for stop and boot recovery, propagated into generated runtime
  configuration and re-attested on every selected node.
- RR-602 hashes the complete generated runtime tree, including file content, type and executable
  mode while excluding the node-private SSH home. Immediately before the current model is stopped,
  Cloudless compares that digest and the actual runtime image identity across every selected Spark;
  source commit and verified model-manifest identities are bound into one persisted per-node
  attestation. A mismatch fails while the previous model is still available.
- RR-603 shows an expandable trust record on every recipe. Exact signed-package profiles are visibly
  distinct from editable local or unverified catalog code. The record exposes the pinned source,
  resolved image digest, compatibility metadata digest, Check evidence, redacted lifecycle commands
  and requested host permissions. Passing compatibility checks never promotes local code to reviewed.
  The main library contains only execution-admitted profiles; local definitions are explicitly
  separated into Drafts. Legacy built-in metadata is migrated only for exact known historical
  values, restoring package provenance without blessing unrelated edits.
- RR-604 exports a no-store ZIP for a specific durable operation containing its sanitized journal,
  recipe commands with secret arguments removed, environment keys without values, live resource
  inventory, selected topology, immutable digests, failure classification and bounded container logs
  from every reachable selected node. Known runtime values and common provider credentials are
  removed from logs, errors and check evidence. Regression tests unzip the artifact and prove that
  arbitrary environment values, command secrets, URL credentials and provider tokens are absent.
- RR-605 exposes the newest unresolved failure directly on the recipe card with the exact failed
  phase, category, redacted detail, affected nodes, retained reusable artifacts, cleanup truth and a
  recommended next action. A later successful operation clears the stale failure. Streaming failures
  no longer display raw `exit status 1`; users are directed to the durable details and can download
  the diagnostic bundle without opening a terminal.
  Doctor additionally detects the concrete failure mode where Cloudless reports inference unloaded
  while a saved recipe container still owns accelerator memory. The user can initiate fixed orphan
  cleanup and receives an attention result until all reachable nodes prove absence.
- RR-606 keeps preparation in the orchestrator rather than the modal lifecycle. Durable operation
  progress is reconstructed after navigation or reload, rendered both on the desktop and in Recipe
  Library, refreshed without repaint blinking, and includes the active transfer route, percentage,
  ETA, details and safe Abort control. Completed downloads and verified artifacts remain reusable.

Exit gate:

- A support bundle can explain every injected failure without requiring terminal access.
- The UI never labels unverified code as Cloudless-verified.
- Secrets are absent from journals, browser payloads and exported diagnostics.

## Release gates

Recipe hardening is not complete until all of these suites pass on both AMD64 and ARM64, with the
distributed cases running on physical DGX Spark hardware:

| Suite | Required coverage |
| --- | --- |
| Unit | State transitions, manifests, port/capacity checks, progress math, command redaction |
| API concurrency | Simultaneous run/check/stop/edit/import/delete requests |
| Failure injection | Every preparation, transfer, start, health, proxy and persistence boundary |
| Restart recovery | Daemon kill and host reboot during every durable phase |
| Storage | Low disk, full disk, corrupt cache, stale marker, interrupted atomic promotion |
| Networking | Peer loss, SSH failure, packet loss, changed IP/interface, rendezvous conflict |
| Runtime | Wrong architecture, mutable-tag drift, missing entrypoint, private-health failure |
| Cleanup | No owned processes, containers, ports or accelerator allocations after failure/abort |
| Compatibility | Single node, two nodes and selected subsets of three-to-eight-node clusters |
| Upgrade | Active recipe and interrupted preparation across a Cloudless package update |

## Delivery order

The recommended implementation sequence is:

1. Phase 0 and the failure-injection harness.
2. Phase 1 so Check stops giving false confidence.
3. Phase 2 before adding more long-running recipe features.
4. Phase 4 cleanup and atomic promotion, with Phase 3 artifact work proceeding in parallel only
   after operation identities and journaling exist.
5. Phase 5 before advertising multi-Spark recipes as production-ready.
6. Phase 6 and final release-gate qualification.

Until Phases 0–4 pass their exit gates, recipe execution should remain explicitly marked
**experimental**. Multi-Spark recipe execution should remain experimental until Phase 5 passes.
