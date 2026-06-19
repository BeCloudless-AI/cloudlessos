# Cloudless — Project Context (read this first)

> This file is auto-loaded into Claude Code's context every session. It is the
> canonical "what is going on" anchor. If you are picking this project up cold,
> read this file top to bottom, then read `docs/STATUS.md` for the live state.

## What we're building

**Cloudless** is a company building **PCs that are ready for local AI**, plus
**CloudlessOS**, a Linux-based distro that ships on those PCs and is also a free
standalone download. CloudlessOS makes installing and running local AI apps
(ComfyUI, vLLM, Ollama, Open WebUI, etc.) effortless — think "Pinokio, but done
properly," with a friendly web UI.

Two products, one software core:
1. **Cloudless PC** — hardware (later phase).
2. **CloudlessOS** — the distro. Ships on the PC (boots into a kiosk web UI) and
   downloads standalone (opens in the user's normal browser). Same backend both ways.

The free OS download is also the marketing engine / top-of-funnel for the hardware.

## The one big reframe (don't lose this)

The product is NOT "a headless Chromium showing a web page." That kiosk browser is
a thin shell. The real product — and all the hard engineering — is three layers
underneath it:

1. **Orchestrator daemon** — local service that installs/runs/updates AI apps,
   manages models, allocates GPU, handles networking. (This is the Pinokio-equivalent.)
2. **Hardware enablement** — correct drivers, CUDA/ROCm, kernel, VRAM detection,
   "recommend models that fit your machine." This is what "ready for local AI" means
   and is the hardest differentiator.
3. **Curated app/model catalog** — vetted recipes that make one-click installs reliable.

## Key decisions so far

See `docs/DECISIONS.md` for full rationale. Summary:
- **Apps run as GPU containers** (Docker/Podman + NVIDIA Container Toolkit), NOT
  Pinokio-style git-clone + conda/venv. Reproducible, isolated, clean uninstall.
- **Shipped distro will likely be immutable/atomic** (bootc / Universal Blue / Fedora,
  à la Bazzite). Not final. Ubuntu/Debian is the easier alternative.
- **Build software first** on commodity hardware → distro image second → hardware last.
- **Phase 0 dev happens on Windows + WSL2** (RTX 5090). The 3-GPU Ubuntu box is
  reserved for multi-GPU / real-hardware testing later.

## Current phase

**Phase 0 — Orchestrator prototype.** Goal: the "magic moment" — click ComfyUI in a
web UI, it installs and runs on the GPU, with working uninstall. See `docs/ROADMAP.md`.

## Dev environment

- **Primary dev box (this machine):** Windows 11, RTX 5090 (32 GB), driver 595.79,
  CUDA 13.2. WSL2 enabled (v2 default). Installing Ubuntu 24.04 into WSL2.
- **Secondary box:** native Ubuntu, 3× NVIDIA GPUs, currently saturated — leave alone for now.
- Full setup + live status: `docs/DEV_ENVIRONMENT.md`.

## Doc map

- `docs/VISION.md` — company & product vision, target users, competitive analogs.
- `docs/ARCHITECTURE.md` — the technical architecture (the 3 layers, in detail).
- `docs/ROADMAP.md` — phased plan with concrete milestones.
- `docs/DECISIONS.md` — decision log (ADR-style) with rationale and open questions.
- `docs/DEV_ENVIRONMENT.md` — hardware, WSL2 setup steps, and current setup status.
- `docs/STATUS.md` — **living** state: what's done, what's in progress, what's next.

## Working agreement

- Keep `docs/STATUS.md` current as work progresses — it's the cold-start anchor.
- Record meaningful technical choices in `docs/DECISIONS.md` so rationale survives.
- Dates in docs are absolute (YYYY-MM-DD), never "today"/"last week".
