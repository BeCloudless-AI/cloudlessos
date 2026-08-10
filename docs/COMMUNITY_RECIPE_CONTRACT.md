# Cloudless community recipe trust contract

This document freezes the Phase 0 wire and trust contract shared by the community service and
CloudlessOS. A change to this contract requires a new schema identifier and new golden fixtures;
silently changing `v1` is forbidden.

## Canonical JSON and signatures

- The signed object uses schema `cloudless.recipe.release/v1`.
- UTF-8 JSON object keys are sorted lexicographically at every depth. Arrays retain their order.
- JSON contains no insignificant whitespace. Strings use JSON escaping for quotes, reverse solidus
  and control characters, but do not HTML-escape `<`, `>` or `&`.
- `null`, booleans and finite JSON numbers are supported. Negative zero is encoded as `0`.
- Recipe schemas constrain integers to small exact values and the only fractional runtime value to
  `gpuMemoryUtilization` in `[0.05, 0.98]`; non-finite and unsafe numeric values are rejected.
- `manifestDigest` is `sha256:` followed by the lowercase SHA-256 of the canonical manifest bytes.
  `cloudless.recipe/v1` remains the constrained managed-container schema;
  `cloudless.recipe/v2` adds the advanced-container execution fields.
- The Ed25519 signature is computed over the canonical UTF-8 bytes of the release envelope without
  its `signature` field. The transmitted signature and SPKI public key are standard base64.
- A manifest is capped at 2 MiB; description 12,000 characters; summary 280; changelog 4,000;
twelve tags of 40 characters; presentation assets 5 MiB and image-only.

`metadata.minimumAcceleratorMemoryGB` is an optional additive compatibility declaration. New
Cloudless publishing clients collect it as a whole number from 1 to 4096. Older manifests without
it remain valid, but their memory requirement is treated as unknown and they are excluded from
memory-fit searches rather than being advertised as requiring zero memory. Architecture is derived
server-side from the admitted platform and never accepted as a browser trust claim.

The golden fixtures under `orchestrator/internal/communityrecipes/testdata` are authoritative and
must pass in Node.js and Go.

## Release envelope

`cloudless.recipe.release/v1` contains exactly:

| Field | Meaning |
|---|---|
| `schema` | Literal release schema identifier |
| `recipeId` | Immutable community recipe UUID |
| `revisionId` | Immutable revision UUID |
| `version` | Author-supplied semantic version |
| `publisherId` | Supabase account UUID that owns the recipe |
| `manifestDigest` | Digest of the nested manifest, recomputed by the service and OS |
| `signingKeyId` | Dedicated community signing key identifier |
| `manifest` | Complete `cloudless.recipe/v1` runtime manifest |
| `signature` | Ed25519 signature added after canonicalization |

Community signing means the service admitted and preserved those exact bytes. It does **not** mean
Cloudless reviewed the recipe. The only approved labels are **Local draft**, **Community signed**,
**Cloudless verified**, **Official**, **Suspended** and **Revoked**.

## State machines

- Recipe: `draft -> published -> suspended|archived`; a suspended recipe may be restored only by a
  moderator. Archival is reversible only by publishing a new admitted revision.
- Revision: `draft -> submitted -> validating -> published`; `draft|submitted|validating` may become
  `rejected`; `submitted|published` may become `withdrawn`; a published revision may become
  `suspended` or irreversibly `revoked` without deleting its forensic history.
- Submission: `queued -> validating -> accepted|rejected|failed`; retries create attempts under the
  same idempotent submission rather than new recipe revisions.

Published, withdrawn, suspended and revoked revision content is immutable.

## Adapter and threat boundary

Public automatic admission supports `managed-container-v1` and `advanced-container-v1`. Both require
an immutable image digest, immutable primary and auxiliary model commits, the fixed loopback
Cloudless inference contract, and no host command, arbitrary host mount, Docker socket, or source
repository. `managed-container-v1` synthesizes a constrained vLLM or SGLang command and remains
local-only. An advanced manifest may request two through
eight enrolled DGX Sparks when tensor parallelism matches node count and it explicitly declares
NCCL, host IPC, and the fixed `/dev/infiniband` permission. Advanced manifests may own the command, entry point, environment,
root filesystem mode, user, IPC, shared memory, ulimits, tmpfs, process limit, and Linux
capabilities plus memory and memory-swap ceilings inside the pinned container. Those permissions are signed and displayed as executable
trust data. `source-scripts-v1` remains private/manual-review only.

Either container adapter may additionally declare one bounded `runtime.smokeTest` program with up
to 128 arguments and a 1-300 second timeout. CloudlessOS executes it against the exact prepared
image ID during on-device validation with no network, GPU, host mounts, or added capabilities; the
container is read-only and has fixed memory, process, and privilege ceilings. This is intended for
dependency and import compatibility checks that should fail before large model downloads.

Controls explicitly cover malicious manifests, mutable dependency replacement, stored XSS,
publisher takeover, leaked keys, replayed mutations, spam and moderator abuse. Server validation
ignores client ownership, digest, compatibility and trust claims; API keys are hashed, scoped,
expirable, immediately revocable and rate-limited; mutations are idempotent and audited; display
text is rendered as text; moderator actions are append-only.

Container admission uses the versioned `actionable-critical-v1` policy. Trivy evaluates all HIGH
and CRITICAL findings for the immutable image digest and retains exact totals plus a bounded
finding sample. HIGH findings and CRITICAL findings without a
known fixed version remain visible warnings. A CRITICAL finding with a known fixed version blocks
publication. An exact container digest already curated and shipped by CloudlessOS can use the
`curated-cloudless-runtime` admission: findings remain visible, but do not independently reject the
same runtime CloudlessOS already distributes. The exception is an explicit digest in reviewed
backend source, not a registry or author allow-list. Scanner errors or incomplete reports fail
closed and are retried; they are never converted into warnings.

## Key custody and rotation

The community Ed25519 private key is separate from the CloudlessOS APT archive key and exists only
in the production API secret store. Public keys are retained by ID. Rotation publishes the new
public key before use and retains old public keys for historical verification. Compromise retires
the key, publishes a signed revocation record with an uncompromised key, suspends affected
revisions pending review and never rewrites their signed bytes.
