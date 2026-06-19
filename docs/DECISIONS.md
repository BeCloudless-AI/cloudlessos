# Decision Log

ADR-style. Newest decisions can go at the bottom. Record the choice, the rationale, and
status. Keep rejected alternatives so we don't relitigate them.

---

## D1 — Apps run as GPU containers, not Pinokio-style installs
**Date:** 2026-06-19 · **Status:** Accepted (revisit engine choice in Phase 0)

Each catalog app runs as a container (Docker/Podman) with GPU passthrough via the NVIDIA
Container Toolkit.

**Why:** Reproducible installs, dependency isolation (no CUDA/Python conflicts between
apps), clean uninstall, straightforward GPU passthrough. Pinokio's git-clone + conda/venv
model is clever but fragile and messy to remove.

**Open sub-decision:** Docker vs Podman (Podman rootless/daemonless is appealing for an
appliance; Docker is more familiar).

---

## D2 — Shipped distro will likely be immutable/atomic (not finalized)
**Date:** 2026-06-19 · **Status:** Leaning, not final

Leaning toward an image-based immutable OS (bootc / Universal Blue / Fedora, à la Bazzite)
for the shipped CloudlessOS.

**Why:** Atomic updates, automatic rollback on bad updates, a system non-technical users
can't easily corrupt — ideal for an appliance shipped to buyers.

**Alternatives:** Ubuntu/Debian (easier to start, broadest driver support); NixOS (most
reproducible, steepest learning curve, philosophically fights runtime app installs).
**Note:** dev base is Ubuntu 24.04 in WSL2 regardless — this decision is about the
*shipped* image, deferred to Phase 1.

---

## D3 — Software first, distro second, hardware last
**Date:** 2026-06-19 · **Status:** Accepted

Build and prove the orchestrator on commodity hardware before building an OS image or PCs.

**Why:** The magic moment is the whole bet. If it isn't magic on a normal box, no distro
or hardware fixes that. Cheap to validate, expensive to get wrong later.

---

## D4 — Phase 0 dev on Windows + WSL2; reserve the 3-GPU box
**Date:** 2026-06-19 · **Status:** Accepted

Prototype on this Windows machine (RTX 5090) via WSL2 + CUDA. The native 3-GPU Ubuntu
box is reserved for multi-GPU / real-hardware testing later.

**Why:** WSL2 now passes the NVIDIA GPU through to a real Ubuntu userspace with working
container GPU support. The 3-GPU box is currently saturated with other workloads — don't
debug against production. (GPUs can time-share later if VRAM allows.)

**Caveat:** WSL2 ≠ bare metal (custom kernel, systemd/networking quirks). Fine for the
daemon/UI/container layers; kernel/boot/init assumptions belong on the real image.

---

## D5 — The kiosk browser is a thin shell, not the product
**Date:** 2026-06-19 · **Status:** Accepted

The value and engineering live in the orchestrator daemon (L1), hardware enablement (L2),
and curated catalog (L3). The headless Chromium kiosk is just presentation.

**Why:** Avoid mistaking the easy 5% for the product. See `ARCHITECTURE.md`.

---

## Open questions (not yet decided)

- **Open-source CloudlessOS?** Leaning yes (trust/community for a privacy brand, like
  Pop!_OS). License TBD.
- **Orchestrator language:** Go vs Python vs Rust.
- **Web UI framework.**
- **Catalog scope:** how curated vs how extensible (defensibility = curation + reliability).
