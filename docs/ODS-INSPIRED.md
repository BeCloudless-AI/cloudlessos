# ODS-inspired Cloudless architecture

This branch turns six operational ideas into one fail-closed system rather than
six independent features.

## 1. Application Manifest v2

`orchestrator/internal/catalog/cloudless-apps-v2.json` is the runtime catalog.
It is embedded in the signed `cloudless-orchestrator` package and strictly
decoded at daemon start. Unknown fields, invalid exposure rules, missing
dependencies, dependency cycles, invalid health contracts, and malformed packs
stop the daemon build/test path instead of reaching Docker.

Each recipe declares architecture/platform support, resources, readiness,
inference routing, dependencies, and network exposure. Public exposure is
fail-closed and requires an authentication contract.

## 2. Hardware-aware model fit

Model Manager estimates weights, quantization, KV cache, runtime overhead, safe
context, and reserved system memory. Dedicated VRAM and unified memory use
different reservations. Multi-node results describe memory required **per
node**, and only count memory as pooled when inference is actually sharded.

These are conservative estimates, not a promise. Unknown model metadata stays
`unknown` rather than being guessed as compatible.

## 3. Bootstrap and verified promotion

On a new appliance whose platform default is large, Cloudless starts a small
bootstrap model first. The full target downloads into the shared model cache
while chat remains usable. Cloudless then performs a serialized handoff through
the stable `cloudless-ai` alias, probes the OpenAI models and chat contracts,
and checks running LLM consumers.

The selected model is persisted only after verification. A launch or contract
failure restores the bootstrap model. Every phase, byte count, attempt, error,
and rollback is durable across reboots. User model/engine/unload/cluster choices
cancel the automatic handoff instead of being overwritten.

## 4. Cloudless Doctor

Settings → Cloudless Doctor checks the container runtime, accelerator, storage,
memory, required services, declared readiness contracts, and model-promotion
state. It is read-only.

The support ZIP is assembled in memory and never uploaded automatically. It
contains bounded recent Cloudless container logs, a state summary, container
status, and the Doctor report. Tokens, passwords, API keys and hashes,
hostnames, and home paths are redacted before ZIP entries are written.

## 5. Release gates

`distro/release/validation-matrix.json` defines the supported production rows:

- generic / AMD64
- generic / ARM64
- DGX Spark / ARM64

The release command runs tests, vet, browser parsing, platform behavior tests,
cross-architecture package checks, package-content checks, isolation checks,
and an atomic repository simulation. The resulting exact-version,
exact-channel, exact-commit matrix attestation is embedded in the signed release
JSON. Signing refuses a missing or mismatched attestation; R2 publication also
rejects a missing, changed, or incomplete gate set.

## 6. Optional capability packs

Voice, private knowledge/RAG, private search, research, and workflow automation
are not boot dependencies. They appear as optional launcher packs. A pack
resolves its dependency graph, checks free storage, serializes container
changes, verifies every declared health contract, and removes newly installed
components in reverse order if setup fails. Pack-owned secrets are generated
locally and stored with daemon-owned state rather than shipped as shared
defaults.
