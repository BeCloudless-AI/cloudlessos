# Roadmap

Principle: **software first, distro second, hardware last.** Prove the magic moment on
commodity hardware before committing to an OS image or building PCs.

## Phase 0 — Orchestrator prototype (CURRENT)

**Goal:** the magic moment. In a web UI, click "ComfyUI" → it installs into a GPU
container, downloads a sensible default model, and opens — with a working uninstall.

Milestones:
1. Dev env ready: Ubuntu 24.04 in WSL2, `nvidia-smi` works inside WSL, container engine
   sees the GPU. (See [[DEV_ENVIRONMENT]].)
2. Manual baseline: run ComfyUI in a GPU container by hand (the thing we'll automate).
3. Orchestrator daemon v0: install/run/stop/uninstall one app via a local API.
4. Minimal web UI: catalog list + install/launch buttons + status.
5. Catalog of 3–4 apps: ComfyUI, Ollama and/or vLLM, Open WebUI.
6. Model manager v0: download + hardware-aware "fits your VRAM" recommendation.

**Exit criterion:** a non-technical person could install and open ComfyUI from the UI
without touching a terminal.

## Phase 1 — CloudlessOS image

Package the daemon + web UI + kiosk shell + drivers into a bootable distro image.

Milestones:
- Choose the distro base (immutable Fedora-family vs Ubuntu) — see [[DECISIONS]].
- Build a branded image (bootc / Universal Blue approach if immutable).
- Kiosk boot flow: boot → daemon up → Chromium kiosk → web UI.
- First-run / onboarding experience.
- Bootable ISO; test installs in a VM (VM is fine here — no GPU needed for image work).
- Standalone download published = top-of-funnel for hardware.

## Phase 2 — Cloudless PC (hardware)

Only once the software is the reason people want the machine.

Milestones:
- Reference hardware spec(s) tuned for local AI.
- CloudlessOS preinstalled and hardware-tuned.
- Support / update story for shipped units.

## Testing-hardware progression

Windows + WSL2 (RTX 5090) for Phase 0 → 3-GPU Ubuntu box for multi-GPU/native testing
→ VM for distro-image iteration → real Cloudless PC reference units in Phase 2.

## See also

- [[VISION]] — why we're building this and for whom
- [[ARCHITECTURE]] — the three-layer technical design being built out
- [[DECISIONS]] — decision log (ADR-style) & open questions (distro base, engine, …)
- [[STATUS]] — living state: what's done, in progress, and next
- [[DEV_ENVIRONMENT]] — hardware, WSL2 setup & runbook for Phase 0
