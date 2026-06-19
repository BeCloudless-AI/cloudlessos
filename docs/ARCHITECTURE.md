# Architecture

> The kiosk/browser is a thin shell. The product is the three layers beneath it.

```
                 ┌─────────────────────────────────────────────┐
   Presentation  │  Kiosk Chromium (on PC)  /  user's browser   │
                 │            → loads local web UI               │
                 └───────────────────────┬─────────────────────┘
                                          │ HTTP/WebSocket (localhost)
                 ┌───────────────────────▼─────────────────────┐
   Web UI        │  Frontend app (catalog, install buttons,     │
                 │  model manager, app dashboards, settings)    │
                 └───────────────────────┬─────────────────────┘
                                          │ local API
                 ┌───────────────────────▼─────────────────────┐
   LAYER 1       │  Orchestrator daemon                          │
   (the brains)  │  - install/run/update/uninstall AI apps       │
                 │  - container lifecycle + GPU allocation       │
                 │  - model download/storage management          │
                 │  - reverse proxy / port & network management  │
                 └───────────────────────┬─────────────────────┘
                                          │
            ┌─────────────────────────────┼─────────────────────────────┐
            ▼                             ▼                             ▼
   LAYER 3                        LAYER 2                       Runtime
   App/model catalog       Hardware enablement          Container engine
   (vetted recipes:        (drivers, CUDA/ROCm,         (Docker/Podman +
   ComfyUI, vLLM,          kernel, VRAM detection,      NVIDIA Container
   Ollama, Open WebUI…)    model-fit recommendations)   Toolkit, GPU passthrough)
```

## Layer 1 — Orchestrator daemon (the product's brain)

A local background service. Responsibilities:
- **App lifecycle:** install, start, stop, update, uninstall AI apps as containers.
- **GPU allocation:** assign GPUs/VRAM to apps; on multi-GPU, pin with
  `CUDA_VISIBLE_DEVICES` / `--gpus`.
- **Model management:** download (Hugging Face, etc.), store, dedupe, track disk usage,
  recommend models that fit detected VRAM.
- **Networking:** reverse proxy each app to a clean local URL; manage ports; keep
  services off the open network unless the user opts in.
- **State & API:** expose a local API the web UI drives; persist installed-app state.

Why a daemon (not just scripts): apps must keep running and be managed independently of
whether the UI is open.

## Layer 2 — Hardware enablement

What makes a box "ready for local AI." Detect GPU(s), VRAM, driver/CUDA/ROCm versions;
ensure the right stack is present; surface "your machine can comfortably run X" guidance.
On Cloudless PCs this is pre-tuned; on standalone installs it must self-configure.

## Layer 3 — App/model catalog

A curated, versioned set of **container recipes** + metadata (VRAM needs, default models,
ports, health checks). Curated (not "install anything") is the defensibility: reliability
over breadth. Initial targets: ComfyUI, Ollama and/or vLLM, Open WebUI.

## Why containers over Pinokio's approach

Pinokio clones repos and juggles conda/venv — clever but fragile, messy to uninstall.
Containers give us: reproducible installs, dependency isolation (no CUDA/Python conflicts
between apps), clean uninstall, and straightforward GPU passthrough via the NVIDIA
Container Toolkit. The catalog = vetted container recipes.

## Presentation shell

- **Cloudless PC:** headless/kiosk Chromium pointed at the local web UI — appliance feel.
- **Standalone:** the daemon serves the same UI; user opens it in their normal browser.

## Distro layer (Phase 1+)

The shipped OS. Leaning toward an **immutable/atomic image** (bootc / Universal Blue /
Fedora, à la Bazzite) for atomic updates, automatic rollback, and a system users can't
easily corrupt. Ubuntu/Debian is the easier-to-start alternative; NixOS is the most
reproducible but steepest. Not finalized — see `DECISIONS.md`.

## Open technical questions

- Daemon implementation language (Go vs Python vs Rust) — undecided.
- Web UI framework — undecided.
- Container engine: Docker vs Podman (Podman is rootless/daemonless, appealing for an
  appliance; Docker is more familiar) — undecided.
- Final distro base (immutable Fedora-family vs Ubuntu) — undecided.

These are intentionally open; revisit during Phase 0 once the prototype reveals constraints.
