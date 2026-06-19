# Vision

## The company

**Cloudless** builds **PCs that are ready for local AI**. The name is the thesis:
your AI runs on your machine, not in someone else's cloud — for privacy, cost
control, offline capability, and ownership.

## The products

### Cloudless PC (hardware)
Local-AI-ready desktops/workstations. Ships with CloudlessOS preinstalled and tuned
to the hardware. (Later phase — software comes first.)

### CloudlessOS (software)
A Linux-based distro whose job is to make local AI **effortless**:
- **On a Cloudless PC:** boots into a kiosk web UI — the machine *is* the AI appliance.
- **Standalone download:** anyone can install CloudlessOS (or run its software layer)
  on their own hardware and get the same friendly experience. Opens in the user's
  normal browser instead of a kiosk.

Same backend in both cases; only the presentation shell differs.

## Who it's for

- People who want local AI but bounce off the current setup pain (CUDA/ROCm versions,
  Python venv/conda breakage, "which model fits my VRAM?", exposing services safely).
- Privacy/sovereignty-minded users and small teams.
- Creators (image/video/audio gen via ComfyUI etc.) and developers (local LLMs/agents).

## Why now

- Capable local-AI hardware just became attainable (NVIDIA DGX Spark, AMD Strix Halo,
  Framework Desktop, affordable high-VRAM consumer GPUs).
- The open models are good enough to be genuinely useful locally.
- The software experience is still miserable — that gap is the opportunity.

## Strategy notes

- The **free OS download is the top-of-funnel** for the hardware. Make it excellent
  and standalone-viable; it earns trust and demand for Cloudless PCs.
- Strongly consider **open-sourcing CloudlessOS** — trust and community are core to a
  privacy-focused local-AI brand. (Open question; see `DECISIONS.md`.)

## Analogs to study

**Business model (hardware + own distro):**
- **System76 / Pop!_OS** — closest analog; a hardware company shipping its own Linux distro.
- **Framework** — community-trusted, repairable hardware brand.

**Software experience:**
- **Bazzite / Universal Blue** — branded, batteries-included immutable Fedora spins
  (model for how to build/maintain CloudlessOS as an image).
- **Pinokio** — one-click AI app installer (what we improve on; we use containers instead).
- **Ollama, LM Studio, Jan, Open WebUI** — local-AI UX leaders; know exactly why we're
  better than each (we orchestrate *many* apps + manage hardware, not one runtime).
