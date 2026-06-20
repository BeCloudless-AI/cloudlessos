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

## D6 — Orchestrator is Go, stdlib-only, Docker via CLI behind an interface
**Date:** 2026-06-19 · **Status:** Accepted

The orchestrator daemon is written in **Go** (chosen over Python/Rust/TS). Phase 0
implementation is **stdlib-only** (no external deps — builds offline) and drives Docker
by **shelling out to the `docker` CLI** behind an `engine.Engine` interface.

**Why Go:** single static binary to ship in an OS image, no runtime to install,
first-class container ecosystem. **Why CLI not SDK (yet):** zero dependencies and fastest
path to the magic moment; the `Engine` interface keeps the Docker SDK or Podman as
drop-in swaps. **Verified:** end-to-end Ollama install/run/stop/remove with GPU attached.

---

## D7 — Web UI is server-embedded, offline-first; Three.js is vendored
**Date:** 2026-06-19 · **Status:** Accepted

The web UI ships as static assets embedded in the daemon binary (`go:embed all:web`).
Third-party libraries are **vendored locally** (Three.js at `internal/api/web/vendor/`),
never loaded from a CDN.

**Why:** An OS/appliance must work fully offline — no runtime network dependency for the
UI. Embedding in the binary keeps deployment to a single artifact.

---

## D8 — First-run / onboarding state is server-side and per-user
**Date:** 2026-06-19 · **Status:** Accepted

The daemon owns first-run state in a JSON file via `internal/state` (atomic temp+rename).
"First launch" = the daemon found no prior state file at startup. The path is per-user by
default (`CLOUDLESS_STATE_DIR` → `$XDG_STATE_HOME/cloudless` → `~/.local/state/cloudless`);
point `CLOUDLESS_STATE_DIR` at a system path (e.g. `/var/lib/cloudless`) for install-wide.
API: `GET /api/onboarding` (`completed`, `firstLaunch`), `POST /api/onboarding/complete`.

**Why:** Browser `localStorage` (the earlier approach) meant "first time" really meant
"this browser profile" — it broke on cache clears, incognito, and different browsers, and
the server had no idea. Server-side state makes "first time this install/user started"
authoritative, surviving browser resets and working in kiosk mode. First store to need
persistence — app/job persistence will likely extend the same package.

---

## D9 — UI design language: blocky, modular, sky/white/black, Red Hat Mono
**Date:** 2026-06-20 · **Status:** Accepted

The home screen is an OS-style **bento grid of modular blocks** (System/clock, Graphics,
Places, App Launcher), each a bordered tile with a monospace `// NN` header. Palette is
**sky blue + white + black** (light theme, `--sky #1f6bff` over a cloudless-sky gradient).
Font is **Red Hat Mono**, vendored locally as woff2 (offline-first, per D7) at
`internal/api/web/vendor/fonts/`. Colors and particle params are CSS variables / constants
for easy tuning.

Backing capabilities added: `internal/hardware` (multi-GPU stats via nvidia-smi →
`GET /api/gpu` returns a `gpus[]` array) and `internal/places` (well-known folders under
`~/Cloudless` (or `$CLOUDLESS_HOME`) → `GET /api/folders`, `POST /api/folders/{id}/open`
via `xdg-open`).

**Why:** "Blocky/modular" matches a monospace, OS-appliance feel and makes each function a
self-contained, rearrangeable tile. Light theme reflects the "cloudless sky" brand.

---

## Open questions (not yet decided)

- **Open-source CloudlessOS?** Leaning yes (trust/community for a privacy brand, like
  Pop!_OS). License TBD.
- **Orchestrator language:** Go vs Python vs Rust.
- **Web UI framework.**
- **Catalog scope:** how curated vs how extensible (defensibility = curation + reliability).
