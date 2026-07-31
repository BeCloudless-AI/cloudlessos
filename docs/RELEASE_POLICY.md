# CloudlessOS release and support policy

This policy applies before CloudlessOS 1.0 and is the minimum bar for declaring 1.0. It does not
replace the technical gates in `distro/release/validation-matrix.json` or the retained evidence
required by [RELEASE_QUALIFICATION.md](./RELEASE_QUALIFICATION.md).

## Release candidates and promotion

1. Cut a release candidate from a pushed, immutable source commit and publish it to `beta`.
2. Retain its source commit, signed release manifest, package hashes, SBOM, vulnerability reports,
   CI run and physical qualification directory together.
3. Run the complete automated matrix and the physical matrix applicable to the changed surfaces.
   A security, signature, boot, rollback, data-integrity or supported-inference failure cannot be
   waived as a known limitation.
4. Keep the candidate in beta for at least seven calendar days. Restart, update, browser/terminal,
   app lifecycle and inference/cluster soak must remain clean throughout that period.
5. Promote only from the same source commit. Stable publication must rerun the production gates and
   public verification. Any source change creates a new candidate and restarts the soak window.

Immutable release manifests, standalone artifacts and their detached signatures are stored under a
version-and-channel namespace. Beta and stable evidence for the same version therefore remain
independently addressable and one channel cannot overwrite the other's qualification descriptor.

The release manifest always carries a signed physical-qualification descriptor. A pre-1.0 build may
state `not-qualified` so development can continue without implying hardware support. A stable 1.0+
release cannot be built unless `cloudless-qualify verify-set` proves every authoritative target ZIP
belongs to that exact version and full source commit; the resulting descriptor is detached-signed
and published with the release artifacts.

Stable signing consumes the exact package and common standalone-artifact bytes from the publicly
verified beta generation. It measures the seven days from the public by-hash object's server
timestamp—not merely the signed manifest creation time—and refuses a younger beta or one whose signed version,
full source commit, platform matrix, gate set, qualification binding, payload hashes or detached
signatures do not match. Stable channel metadata, its physical-qualification identity and detached
signatures are regenerated; package payloads, SBOM, catalogs, trust inventory and installer bytes
remain identical to beta.

## Rollback retention and response

- Keep the previous two complete stable generations publicly available for at least 90 days after
  their successor ships.
- Do not garbage-collect a rollback package, manifest, SBOM or signature while a supported installed
  generation can still reference it.
- A stable release must prove rollback on every architecture before publication. Physical update
  continuity is required for changes touching boot, display, inference, storage or cluster state.
- For a confirmed critical regression, target a signed rollback or fixed candidate within 24 hours.
  Never overwrite an already published package object; publish a new signed generation.

## Product support levels

- **Supported**: covered by the documented compatibility contract and required release matrix.
  Regressions block promotion. During the pre-1.0 period, target initial triage within three business
  days and a fix, rollback or documented mitigation within 30 days.
- **Preview**: intended for real evaluation and covered by contract/unit tests, but may lack the full
  physical matrix. Target initial triage within seven business days; no resolution deadline is
  guaranteed.
- **Experimental**: locally built, editable, unreviewed or intentionally incomplete functionality.
  It is isolated and labeled, but compatibility and resolution timelines are not guaranteed.

Security reports affecting a currently supported release target acknowledgement within three
business days. These are engineering targets, not a paid uptime or availability guarantee. Before
1.0, Cloudless must publish a monitored security contact and an escalation owner; absent those,
CloudlessOS remains a pre-release product.

## Telemetry and privacy

CloudlessOS has no product analytics or automatic support-bundle uploader. Enabling update checks,
registries, Hugging Face, Tailscale, Cloudflare tunnels, the unrestricted browser or a third-party
application creates the explicit network traffic described in [PRIVACY.md](./PRIVACY.md). A release
must not add telemetry, crash upload or remote diagnostics without:

1. an off-by-default user control and clear destination/purpose disclosure;
2. data minimization and a documented retention period;
3. a local view/delete path where technically possible; and
4. an updated threat model, privacy guide and release note.

## 1.0 decision

CloudlessOS may be called 1.0 only when the full automated and physical matrices are green, a
rollback rehearsal succeeds, the beta soak completes, remaining limitations are visible in-product,
the security contact is monitored and the response ownership above is operational.
