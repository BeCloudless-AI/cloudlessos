# Cloudless community recipes roadmap

This document is the implementation source of truth for authenticated recipe authorship,
publication, discovery and community activity. It extends the machine-local
`cloudless.recipe/v1` system described in [`LOCAL_RECIPES.md`](./LOCAL_RECIPES.md); it does not
replace the local recipe store or the reliability requirements in
[`RECIPE_RELIABILITY_ROADMAP.md`](./RECIPE_RELIABILITY_ROADMAP.md).

For the user-facing creation, publication, status, discovery, and installation workflow, see
[`COMMUNITY_RECIPES.md`](./COMMUNITY_RECIPES.md). This roadmap intentionally keeps engineering and
rollout details out of that practical guide.

The work is ordered by dependency. Identity, immutable revisions, authorization and executable
policy must be complete before discovery, ratings or comments can make a recipe look trustworthy.

## Delivery status (2026-08-02)

Phases 0-5 are implemented in source and the live Supabase schema. The production CentOS backend
now passes the authenticated managed-recipe publication path, including asynchronous validation,
public discovery, exact-version retrieval and cryptographic signature verification. Phase 6
operational tooling is implemented, but public rollout and physical launch evidence are intentionally not marked complete:
the deployed backend revision is now source-bound and ready; completion still requires assigned
moderators, a closed beta, and AMD64/one-Spark/
two-Spark qualification. Supabase leaked-password protection must also be enabled in the project
dashboard before public registration is promoted. Local validation uses repository scripts and
does not require paid GitHub Actions.

The public API is deployed: health, readiness, catalog, signing-key and revocation endpoints return
200. Production reports source digest
`51f0bcf7905670a9741dd9e3ec7259d71686a68719d3b151d467337b53586f8a`, matching the audited
backend source exactly. The exact-release public-read rehearsal completed 501 requests with zero
errors and is retained at `backend/evidence/community-load/2026-08-01T22-13-50-484Z.json` in the
backend workspace (SHA-256
`2385e6bb301f15bd055de83f038f5f276466cd86fc8c9a275288cfe949762147`). Its p95 latency was
457 ms for catalog search, 455 ms for the signed revocation feed and 459 ms for signing keys.
The authenticated rehearsal also proved API-key creation/authentication/revocation, draft creation,
immutable revision creation, submission, publication, discovery, exact-version download, withdrawal
and cleanup against production. The published Ed25519 signature was independently verified against
the public `community-2026-08` trust key.

That authenticated rehearsal found and resolved four deployment blockers:

- Submission polling and worker/moderation queries used an ambiguous PostgREST relationship after
  `recipes.current_revision_id` introduced a second relationship to `recipe_revisions`. Source now
  binds every embed to `recipe_revisions_recipe_id_fkey`, with a regression contract test.
- The production validation job remained queued with zero attempts because the validation-worker
  service was not running. Source now defaults API-only deployments to an embedded worker. The
  atomic deployment selects the isolated worker mode and enables, starts and verifies both systemd
  services across VPS reboot. The live embedded-worker rehearsal then correctly claimed the job and
  exposed a missing Trivy executable. Source now bootstraps Trivy 0.70.0 for Linux x64/ARM64 from
  hard-coded, checksum-pinned release archives when `TRIVY_PATH` is absent. The bootstrap extracts
  the archive with Node.js streams and built-in zlib rather than relying on a distro-specific `tar`
  executable, which was verified on the production CentOS host. Retry exhaustion also rejects the
  revision instead of leaving it indefinitely `validating`.
- The first real NVIDIA vLLM sample passed the local manifest policy but production rejected it
  because Trivy's severity exit code treated every HIGH and CRITICAL finding as fatal. The worker
  now owns a versioned `actionable-critical-v1` decision: every finding is retained, HIGH and
  currently unfixable CRITICAL findings are visible warnings, actionable CRITICAL findings block,
  and scanner failures still fail closed. An exact, code-reviewed image digest already shipped by
  CloudlessOS can be admitted as a curated runtime while retaining all findings as warnings. Unit
  tests bind these cases and submission status exposes the policy and summary to the author.

The successful disposable recipe was withdrawn and removed after verification, its temporary API
key was revoked, a subsequent authenticated request returned 401, and the public catalog returned
zero matching cleanup artifacts.

The production preflight now validates Node 22+, production mode, signing identity, Supabase Auth,
writable scanner/cache state and checksum-pinned Trivy availability before either systemd service
starts. The API and isolated worker units use package-owned state directories and no longer assume
that CentOS supplies a system `tar` or `trivy`. A signing rehearsal independently verified the live
key, tamper rejection and the packaged CloudlessOS trust root; retained evidence is
`backend/evidence/community-trust/2026-08-01T21-20-14-008Z.json`. CloudlessOS handler tests now
exercise a complete exact-release install and prove that unknown signing keys, post-signature
tampering and an exact signed revocation all fail before the local recipe store changes.

Release evidence is now bound in both directions. The backend computes an immutable identity from
its runtime source and dependency lock files at startup; an optional matching `release.json` can add
a human-readable release name but cannot override that digest. `/api/health` exposes the non-secret
release ID, source-tree SHA-256 and active community access stage. This works with either manual
file upload plus restart or automated deployment. Load and Phase 6 audit tooling refuse to treat an
HTTP-healthy but source-mismatched server as the release under test, closing the earlier possibility
of labeling an older live backend with the digest of newer local source.

Encrypted logical backup no longer depends on a host `tar` package. A bounded versioned v2
container streams the PostgreSQL custom dump with a manifest, validates exact payload length on
unpack, and is then encrypted with `age`. Isolated restore verifies the core community tables,
runs a database smoke query and writes a secret-free evidence record; the actual isolated restore
rehearsal remains an operational gate.

Closed-beta write access is enforced in application authorization, not only in the interface:
production defaults to `COMMUNITY_ACCESS_STAGE=closed-beta`; ordinary confirmed sessions remain
read-only, testers may publish, and general session publishing requires an explicit later switch to
`public-publishing`. API-key issuance remains tester-only. The deployed trust-key response exposes
the stable `publicKey` contract; clients continue to accept the legacy `public_key` spelling during
the compatibility window.

The backend was uploaded manually and restarted; no packaging or deployment helper is required for
release identity. The source-bound audit now passes against `https://becloudless.ai/api`, and
`/api/ready` confirms an exact-release validation worker heartbeat, reachable queue and zero pending
dead-letter jobs.

Current local verification for this Phase 6 candidate is green: 63 backend tests, the complete Go
suite, 37 physical-qualification contract tests, five exact-commit qualification tests, and the
Debian install/upgrade/rollback lifecycle all passed on 2026-08-02. The current signing rehearsal
also verified the live `community-2026-08` key, exact-byte signatures, tamper rejection and the
packaged CloudlessOS keyring; its evidence is
`backend/evidence/community-trust/2026-08-01T22-16-27-102Z.json`. These prove the implementation,
not the outstanding production rollout or physical qualification gates.

The two-Spark physical campaign for installed candidate `0.2.7-1` and source commit
`ac8eb9df913ec7e0dd55fe5e74fbb88283156825` has started on coordinator `spark-3493`. Its first
machine-authenticated graphical boot, redacted support bundle, fabric/restricted-SSH preflight and
selected two-node topology are retained as campaign evidence. The run exposed an actual packaging
defect: `cloudless-firstboot.service` was wanted by `multi-user.target` while waiting for
`graphical.target`, so systemd could discard the ordering cycle and omit the boot audit. Source now
anchors the audit to `graphical.target`, removes the cyclic dependency, and reenables the unit on
upgrade so the obsolete symlink is removed. The exact candidate still requires the remaining boot,
interactive, lifecycle and injected-failure evidence before it can produce a sealed export.

Runtime readiness now closes the failure mode seen during the first production rehearsal, where
the HTTP API was healthy while no validation worker was processing submissions. Every worker
publishes a short database heartbeat bound to its exact source digest, even during a long image
scan. `/api/ready` fails closed when that heartbeat is stale or belongs to another backend release,
or when the migration or queue state is unavailable. Dead-letter jobs remain visible as moderator
attention and block promotion without making an otherwise healthy public catalog return 503.
The heartbeat table and atomic dead-letter resolution function are applied to the live Cloudless
Supabase project with RLS enabled. The function is `SECURITY INVOKER`, executable only by the
server role, and performs retry/acknowledgement plus append-only moderation audit insertion in one
database transaction.

### Implementation map

- Cloudless trust/install/runtime: `orchestrator/internal/communityrecipes`,
  `orchestrator/internal/api/community_install.go`, and `orchestrator/internal/localrecipes`.
- Cloudless author/discover/social UI: `orchestrator/internal/api/web/index.html`.
- Packaged trust root: `distro/release/keys/community-keys.json`.
- Account service (the separate becloudless backend workspace): `src/routes/recipes.js`,
  `src/routes/moderation.js`, `src/community`, `src/validationWorker.js`, and `openapi.yaml`.
- Supabase history: `supabase/migrations/20260801180634_community_recipes_foundation.sql` through
  `20260801235500_community_catalog_unknown_memory.sql` in that backend workspace.
- Operations without paid CI: backend `npm run check:community`, `npm run load:community`,
  `scripts/backup-community.sh`, and `scripts/restore-community-rehearsal.sh`. Load evidence is
  timestamped, hashed, and bound to the deterministic backend source-tree digest. Atomic deployment
  uses that same digest in its immutable release identifier and `release.json`.

## Product contract

A user can create and validate a recipe locally without an account. Signing in is required only
for server-owned actions such as publishing, editing a public listing, commenting, rating,
reporting or managing an author's submissions.

There are two supported publishing clients:

1. CloudlessOS uses the signed-in Supabase session through the loopback `cloudlessd` account and
   community gateway.
2. External tools use a user-created `cld_alpha_...` API key through the same public REST API.

Both clients reach the same service layer, validators, ownership checks, rate limits, audit log
and immutable revision writer. An API key is not a privileged bypass.

Published recipes contain manifests and small presentation assets only. Cloudless never uploads
model weights, model cache content, custom Docker images, Hugging Face credentials, local paths,
SSH material, environment secret values or support bundles.

## Trust vocabulary

These states must stay separate in the data model and interface:

- **Local draft**: exists only on one machine and has no community provenance.
- **Community published**: an authenticated account published this exact immutable revision and
  the community service signed its canonical bytes.
- **Policy approved**: the revision uses an adapter that the installed Cloudless version can admit
  without arbitrary host execution. Initially this means `managed-container-v1` only.
- **Author tested**: the author submitted a successful local validation for the exact revision and
  disclosed platform. This is an author claim, not remote attestation.
- **Cloudless verified**: Cloudless retained matching validation and launch evidence on declared
  supported hardware for the exact revision.
- **Official**: Cloudless owns and maintains the recipe.

A signature proves publisher identity and content integrity; it does not prove safety or runtime
compatibility. Ratings and download counts do not change executable trust.

`source-scripts-v1` definitions may be submitted privately for manual review, but they must not
appear in Discover or become installable until an exact definition is admitted by a signed
Cloudless package. The public catalog must not contain attractive cards that users cannot install.

## Immutable publication envelope

The existing executable manifest remains `cloudless.recipe/v1`. Publication adds a separate
envelope so community metadata can evolve without changing the runtime schema:

```yaml
schema: cloudless.recipe.release/v1
recipeId: 00000000-0000-0000-0000-000000000000
version: 1.0.0
publisherId: 00000000-0000-0000-0000-000000000000
manifestDigest: sha256:...
signingKeyId: community-2026-01
signature: ...
manifest:
  schema: cloudless.recipe/v1
  metadata: {}
  recipe: {}
```

Published revisions are append-only. Editing creates a new semantic version and changelog. A
recipe installation always records the recipe ID, revision ID, semantic version, digest and
signing key ID. Cloudless never silently replaces an installed executable revision.

The online community signing key is separate from the APT archive key. The corresponding trust
root is shipped by a signed CloudlessOS package. Key rotation and emergency revocation therefore
cannot grant authority to publish operating-system packages.

## Proposed data model

Every table exposed through Supabase must have RLS enabled, explicit `anon`/`authenticated` grants
and ownership policies. The API must not assume new public tables are automatically exposed.

| Record | Purpose |
|---|---|
| `community_profiles` | Public username, avatar, display name and biography; never account email |
| `recipes` | Stable identity, owner, slug, category, visibility, lifecycle state and current revision |
| `recipe_revisions` | Immutable version, canonical manifest, digest, signature, compatibility summary and changelog |
| `recipe_validation_runs` | Exact-revision author or Cloudless evidence with platform and result |
| `recipe_stars` | One saved-recipe relationship per account |
| `recipe_ratings` | One editable rating per account and recipe |
| `recipe_comments` | Threaded comments with edit and soft-delete state |
| `recipe_reports` | User reports with category, explanation and resolution state |
| `moderation_actions` | Private append-only moderator audit history |
| `recipe_audit_events` | Private publication, key use, revision and status-transition audit events |

`public.profiles` currently contains private account fields and must not be made globally readable.
Community-facing profile data belongs in `community_profiles` or a security-invoker public view
that cannot expose email or authorization attributes.

Small screenshots and documentation images live in a dedicated Storage bucket with content-type,
count and size limits. Manifests, signatures and moderation state remain authoritative database
records. Storage policies restrict writes to an owner's recipe prefix; replacing an object requires
the matching SELECT, INSERT and UPDATE policies.

## API contract

The first public API surface should be versioned from the beginning:

```text
GET    /api/v1/recipes
GET    /api/v1/recipes/:slug
GET    /api/v1/recipes/:slug/revisions/:version

POST   /api/v1/recipes
PATCH  /api/v1/recipes/:id
POST   /api/v1/recipes/:id/revisions
POST   /api/v1/recipes/:id/revisions/:revisionId/submit
POST   /api/v1/recipes/:id/revisions/:revisionId/withdraw

GET    /api/v1/me/recipes
GET    /api/v1/me/recipe-submissions

PUT    /api/v1/recipes/:id/star
DELETE /api/v1/recipes/:id/star
PUT    /api/v1/recipes/:id/rating
GET    /api/v1/recipes/:id/comments
POST   /api/v1/recipes/:id/comments
PATCH  /api/v1/comments/:id
DELETE /api/v1/comments/:id
POST   /api/v1/recipes/:id/report
```

CloudlessOS proxies these routes through `cloudlessd` so the kiosk remains same-origin. External
clients call `https://becloudless.ai/api/v1/...` directly.

Every mutation accepts an idempotency key. List routes use stable cursor pagination. Error bodies
include a machine-readable code, safe message and field errors. Publication never performs a
long-running validation in the request: it returns a submission ID and status that clients poll.

## Phase 0 - Freeze contracts and threat model

- [x] Document canonical JSON generation, digest input, signature encoding and size limits with
  shared golden fixtures for Go and Node.js.
- [x] Freeze `cloudless.recipe.release/v1` and map every field to `cloudless.recipe/v1`.
- [x] Define recipe, revision and submission state machines, including allowed transitions.
- [x] Define the trust labels above and approve their exact interface language.
- [x] Threat-model malicious manifests, publisher takeover, leaked API keys, replayed submissions,
  mutable images, dependency replacement, stored XSS, spam and moderator abuse.
- [x] Decide the initial community signing-key custody, rotation and revocation procedure.
- [x] Cap manifest, changelog, description, tag and asset sizes before schema implementation.

Done gate:

- Go and Node produce identical canonical bytes and digests for every fixture.
- No state name or badge can imply that community signing equals Cloudless verification.
- Security review approves the adapter boundary and signing-key separation.

## Phase 1 - Identity, API-key publishing and immutable backend

Phase 1 includes external publishing. It is not complete while API keys can only be created and
revoked without authenticating recipe endpoints.

### Publishing principals

- [x] Introduce one request principal abstraction for Supabase sessions and `cld_alpha_...` keys.
- [x] Detect API keys in `Authorization: Bearer cld_alpha_...`, hash them and resolve only active,
  unexpired records through the server-side client.
- [x] Add explicit API-key scopes: `recipes:read`, `recipes:write` and `recipes:publish`.
- [x] Default new publisher keys to recipe scopes only. They must not edit profiles, delete
  accounts, administer keys, comment, rate or moderate.
- [x] Return the raw key only once, retain only its cryptographic hash and display metadata, and
  support immediate revocation and optional expiry.
- [x] Update `last_used_at` with bounded write frequency rather than on every request.
- [x] Rate-limit by key, account and source address; never include the raw key in logs or errors.
- [x] Keep tester-only key issuance during the private beta. Define the later email-verification,
  anti-abuse and account-standing gate before opening issuance to every account.
- [x] Add positive and negative tests for scope denial, ownership, expiry, revocation, rotation,
  malformed prefixes, rate limits and cross-account access.

### Backend records and authorization

- [x] Add `community_profiles`, `recipes`, `recipe_revisions`, `recipe_validation_runs` and
  `recipe_audit_events` with explicit grants, RLS and indexes.
- [x] Keep public profiles separate from private email and authorization fields.
- [x] Permit owners to mutate drafts while making submitted and published revisions immutable.
- [x] Create the backend recipe service once and call it from both session and API-key routes.
  Do not maintain two publishing implementations.
- [x] Canonicalize and validate manifests on the server; never trust client-calculated digests,
  compatibility summaries, ownership IDs or trust labels.
- [x] Accept public publication only for constrained `managed-container-v1` revisions with pinned
  model revisions and immutable image digests.
- [x] Store private `source-scripts-v1` submissions for review without listing or install URLs.
- [x] Sign accepted canonical revisions with the dedicated community key and persist key ID,
  digest and signature atomically.
- [x] Add idempotency records so a client retry cannot create duplicate revisions.
- [x] Add audit events for key use, draft creation, submission, publication, withdrawal and
  administrative status changes.

### Phase 1 API and tooling

- [x] Implement recipe create, revision create, submit, status and owner-list routes.
- [x] Publish an OpenAPI document and generated examples for session and API-key authentication.
- [x] Provide a minimal external CLI workflow:

  ```bash
  cloudless recipes validate ./cloudless-recipe.yaml
  cloudless recipes publish ./cloudless-recipe.yaml --api-key "$CLOUDLESS_API_KEY"
  cloudless recipes status <submission-id>
  ```

- [x] Add secret-redaction tests covering HTTP logs, validation failures and support bundles.
- [x] Run Supabase security and performance advisors after the schema stabilizes.

Done gate:

- The same exact manifest published with a session or properly scoped API key produces the same
  canonical digest and service behavior.
- Revoking a key prevents its next request without waiting for a JWT refresh.
- Cross-account draft and revision access is denied in route tests and direct database tests.
- A mutable image tag, floating model revision, arbitrary command or forbidden permission cannot
  become a public installable revision.
- A published revision is immutable, signed and retrievable by exact version and digest.

## Phase 2 - Cloudless authoring and publication experience

- [x] Add **My recipes** and **Create recipe** beside Installed and Discover.
- [x] Keep local creation and saving available while signed out.
- [x] Require sign-in only when Publish or another community mutation begins.
- [x] Build a four-step publication wizard: presentation, compatibility, security disclosure, then
  version/changelog confirmation.
- [x] Show exact image digest, model revision, resources, topology, network, filesystem and device
  permissions before submission.
- [x] Run local validation before Publish and attach exact-revision author evidence without
  presenting it as remote verification.
- [x] Display backend field errors inline and preserve the local draft after every failure.
- [x] Show submission states without blinking or blocking the rest of Cloudless.
- [x] Allow a published recipe to create a new revision, never edit an old one.
- [x] Add account key management with one-time secret display, scopes, expiry, last use and revoke.

Done gate:

- A signed-in user can author, validate, publish and inspect a revision without leaving Cloudless.
- Closing Model Manager does not cancel submission or lose its status.
- Signing out preserves local drafts and removes access to private server drafts.
- Accessibility, responsive layout and virtual-keyboard checks pass on AMD64 and DGX Spark.

## Phase 3 - Discovery, installation and updates

- [x] Add Discover search with category, model, engine, architecture, accelerator-memory, node-count
  and trust filters.
- [x] Use PostgreSQL full-text/trigram search initially; do not reintroduce a separate crawler or
  recipe indexer.
- [x] Build a recipe detail view with publisher, exact current version, compatibility, permissions,
  changelog, trust record and report action.
- [x] Download the exact release envelope, verify its signature and digest inside `cloudlessd`, then
  apply the installed-version policy before importing it into the local store.
- [x] Reject unknown signing keys, revoked revisions, schema versions newer than the installed
  client and adapters the client cannot constrain.
- [x] Keep Install, model download, Validate and Run as separate visible steps.
- [x] Record installed recipe ID, revision and digest locally and show explicit updates.
- [x] Never auto-update an executable revision; preserve rollback to the previously installed
  revision where its referenced artifacts remain available.
- [x] Cache catalog reads for offline browsing while preventing stale cache from overriding a
  revocation check before a new install.

Done gate:

- A clean Cloudless install can discover, verify, install, validate, run, stop, update and roll back
  a policy-approved community recipe.
- Tampered manifests and signatures fail before the local recipe store changes.
- Offline state clearly distinguishes cached listings from installable verified revisions.
- Existing local recipes continue to work without an account or community connection.

## Phase 4 - Social layer

- [x] Add stars, ratings, comments and public author pages.
- [x] Limit each account to one editable rating per recipe.
- [x] Mark whether a rating author installed or locally validated an exact revision without exposing
  device identity.
- [x] Add threaded comments, editing, soft deletion, pagination and author blocking.
- [x] Add notification records for replies, new revisions, verification results and moderation.
- [x] Add recipe reports with spam, unsafe behavior, malware, misleading compatibility, license and
  impersonation categories.
- [x] Add aggregate counts without collecting persistent machine identifiers. Telemetry beyond
  explicit community actions remains opt-in.
- [x] Add anti-spam limits and abuse heuristics before public registration is promoted.

Done gate:

- Ownership is enforced for every rating/comment mutation and moderator actions are auditable.
- Stored content is safely rendered and cannot inject markup or script into Cloudless.
- Social popularity never changes executable admission or verification status.

## Phase 5 - Moderation and Cloudless verification

- [x] Build a moderator queue for submissions, reports, revisions and account standing.
- [x] Require a reason and append-only audit record for every moderation action.
- [x] Add suspend, withdraw and revoke states without deleting forensic history.
- [x] Publish a signed revocation feed consumed before new installs and during update checks.
- [x] Add automated schema, secret, dependency, license, image and vulnerability checks without
  relying on paid GitHub Actions.
- [x] Run validation workers owned by Cloudless and retain exact recipe, image, model, OS, hardware
  and topology evidence.
- [x] Define separate verification matrices for generic NVIDIA AMD64, one DGX Spark, two DGX Sparks
  and preview three-to-eight-Spark configurations.
- [x] Let maintainers mark an exact revision Cloudless verified only from retained passing evidence.
- [x] Define appeals, coordinated disclosure and emergency takedown procedures.

Done gate:

- A revoked revision cannot be newly installed and is visibly flagged on machines where it already
  exists, without silently deleting local data.
- Cloudless verified always identifies the exact tested revision and platforms.
- The moderation and signing services have backup, restore, key-rotation and incident rehearsals.

## Phase 6 - Public launch and scale

- [ ] Execute the supplied staging load test for search, publishing, comments, signature downloads
  and key authentication, and retain the result. Public reads, API-key authentication, draft and
  immutable revision creation, managed validation/publication, discovery, exact signature download,
  independent signature verification, withdrawal and key revocation have passed in production.
  The interactive-session comment path still needs retained load evidence.
- [x] Add bounded queues and retry/dead-letter handling for asynchronous validation.
- [x] Establish database, Storage and signing-service backup/restore objectives.
- [x] Publish API stability, deprecation, rate-limit and responsible-disclosure policies.
- [x] Publish CLI documentation and examples for external recipe authors.
- [ ] Run a closed tester beta, then a public read-only catalog, then public publishing.
- [ ] Retain launch evidence across backend, AMD64, one-Spark and two-Spark targets.

### Remaining operational gates

1. **Completed:** the manually uploaded backend exposes the automatically derived immutable release
   identity/access stage. Health and readiness match the local source digest, the exact-release
   worker heartbeat and queue are healthy, the public trust-key response exposes `publicKey`, and a
   source-bound 501-request read rehearsal completed with zero failures. Retain the VPS startup/
   preflight log with the release evidence when the launch record is sealed.
2. Enable Supabase leaked-password protection, appoint at least two moderators, and rehearse
   revocation plus encrypted restore in an isolated database. Moderator staffing is accepted only
   from a direct server-side Supabase count or the authenticated moderator-readiness endpoint, not
   an operator-entered count. Leaked-password protection is accepted only from the Supabase
   Security Advisor. As of 2026-08-02, that advisor reports protection disabled and the live
   `community_moderators` table contains zero rows. The same advisor currently reports no other
   warning/error finding; informational unused-index notices are expected before catalog traffic
   and must be reevaluated after the beta rather than removed prematurely.
3. Run the authenticated-session comment mutation under the load harness and retain cleanup and
   error-rate evidence. Public-read load, signing trust, API-key publication and key revocation are
   already retained.
4. Qualify signed discover/install/update/rollback on generic AMD64, one Spark and two Sparks.
   The Phase 6 audit accepts only complete sealed qualification ZIPs that pass `cloudless-qualify
   verify-export` and match the expected target; loose evidence files cannot satisfy this gate.
   Three-to-eight Sparks remain preview and are not a blocker for the advertised matrix.
5. Progress closed tester beta -> public read-only -> public publishing only when the preceding
   evidence is retained. Each stage requires a hashed load report bound to the stage and immutable
   live release; merely changing `COMMUNITY_ACCESS_STAGE` cannot satisfy the audit. Do not bypass a
   gate by changing a database status manually.

Done gate:

- Publication failure cannot corrupt or partially expose a revision.
- Restore and signing-key rotation rehearsals pass.
- Abuse, moderation, support and incident ownership are assigned before public write access opens.

## Required test matrix

Every phase adds tests at the lowest applicable layer:

| Layer | Required coverage |
|---|---|
| Canonical fixtures | Node/Go equivalence, Unicode, ordering, numbers, unknown fields and size bounds |
| Database | grants, RLS ownership, immutable revisions, unique versions and status transitions |
| Authentication | session/API-key parity, scopes, expiry, revocation, rate limits and secret redaction |
| Backend | validation, idempotency, signing, pagination, moderation and failure recovery |
| Cloudless API | same-origin proxy, token refresh, signature verification and offline behavior |
| UI | authoring, publishing, discovery, installation, updates, errors and accessibility |
| Runtime | managed-container policy, local Validate/Run/Stop/Abort and stable inference contract |
| Physical | generic NVIDIA AMD64, one Spark and two Sparks for advertised compatibility |

Three-to-eight-Spark recipes remain preview until matching hardware evidence can be retained. A
community author claiming a larger topology does not upgrade the Cloudless support level.

## Initial implementation order

When work starts, use this exact sequence:

1. Canonical fixtures and release envelope.
2. API-key scope migration and dual-principal authentication middleware.
3. Core recipe/revision/audit schema with RLS and grants.
4. One shared publication service and API route tests.
5. Dedicated signing key and verification fixture.
6. External CLI publish/status smoke test.
7. CloudlessOS community proxy.
8. My Recipes and Publish wizard.
9. Discover and signed installation.
10. Social and moderation phases only after the publication/install gates pass.

Do not begin ratings, comments, follows or generalized discovery while Phase 1 permits an
unauthorized, mutable or unadmitted executable revision to become publicly installable.
