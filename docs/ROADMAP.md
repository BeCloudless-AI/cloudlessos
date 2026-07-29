# CloudlessOS roadmap

The original orchestrator and installer prototypes are complete enough to test on real AMD64
hardware, VirtualBox and DGX Spark. The roadmap now focuses on reliability, extensibility and a
production-ready distribution rather than proving that a local web control plane can work.

## Current — pre-release hardening

- Keep the AMD64 installer and DGX Spark ARM64 package layer on one signed release generation.
- Validate updates, rollback, first boot, display/kiosk recovery and NVIDIA driver behavior on a
  wider hardware matrix.
- Finish the managed application catalog and remove placeholder or unqualified recipes.
- Improve model compatibility data, load progress, diagnostics and failure recovery.
- Harden two-to-eight-Spark configuration, monitoring and distributed inference.
- Validate Hermes Agent permissions, persistence, integrations and scoped API access.
- Exercise custom source-built vLLM/SGLang registration, readiness and managed-engine rollback.
- Complete licensing, security review and public installation/support documentation.

## Next — extensibility without fragility

- Signed native recipe publishing and a unified Cloudless community identity service.
- Recipe ownership, ratings, comments, moderation and reproducible revision pins.
- More inference compatibility bases where they add real value.
- Controlled custom-engine support for Spark clusters, including image distribution and
  per-node compatibility checks.
- Better developer diagnostics, benchmark comparisons and exportable engine profiles.
- Recovery media and offline/factory installation options.

## Later — Cloudless hardware

- Reference Cloudless PC configurations validated against the same public CloudlessOS build.
- Multi-accelerator tuning, acoustics, power and thermal profiles.
- Factory provisioning, recovery and support lifecycle.
- Hardware-specific capability packages only where a shared signed contract is insufficient.

## Engineering principles

- Software and update reliability precede hardware expansion.
- One source tree and release identity cover supported architectures.
- Platform differences are backend capabilities, not frontend forks.
- Exactly one inference engine owns the stable API endpoint at a time.
- Managed paths remain recoverable even when advanced users experiment with custom code.
- Public discovery must include provenance, revisions, validation and moderation rather than
  becoming an unreviewed script index.

## See also

- [Live status](./STATUS.md)
- [Architecture](./ARCHITECTURE.md)
- [Custom engine guide](./CUSTOM_ENGINES.md)
- [Decision log](./DECISIONS.md)
- [DGX Spark operations](../distro/DGX-SPARK.md)
