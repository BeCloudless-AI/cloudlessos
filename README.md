# Cloudless

**PCs that are ready for local AI — and the OS that makes it effortless.**

Cloudless builds local-AI-ready PCs and **CloudlessOS**, a Linux distro that turns
installing and running local AI (ComfyUI, vLLM, Ollama, Open WebUI, …) into a
one-click, friendly experience. CloudlessOS ships on Cloudless PCs and is a free
standalone download for anyone who wants easy local AI on their own machine.

## Status

Early development — **Phase 0: orchestrator prototype**. The daemon + web UI already
work: install and run AI apps as GPU containers, browse and switch models, and chat with
your local AI. The bootable CloudlessOS **ISO isn't built yet** — for now you run it
straight from this repo (see below).

## Try it now (before the ISO) — the ELI5 version

CloudlessOS is really two things: a small background program (`cloudlessd`) and a web
page it serves. Until the bootable ISO exists, **you start that program yourself and open
the page in your browser** — that page *is* CloudlessOS.

**What you need**

- An **NVIDIA GPU**.
- **Linux** (Ubuntu 22.04 / 24.04) — or **Windows 11 with WSL2** Ubuntu.
- **Docker** + the **NVIDIA Container Toolkit** (lets containers use your GPU).
- **Go 1.26+** (to build it).

**Steps**

1. **Get the code**
   ```bash
   git clone https://github.com/samuelcardillo/cloudlessos.git
   cd cloudlessos
   ```
2. **First time only — install the prerequisites.** On Ubuntu/WSL2 the helper scripts do
   it for you (they install system packages, so they'll ask for your password). Already
   have Docker + NVIDIA Container Toolkit + Go? Skip this step.
   ```bash
   bash scripts/setup-wsl-docker.sh   # Docker + NVIDIA Container Toolkit
   bash scripts/install-go.sh         # Go 1.26 toolchain
   ```
3. **Start it**
   ```bash
   cd orchestrator
   go run ./cmd/cloudlessd
   ```
   The first launch pulls a few container images and a small default model, so give it a
   couple of minutes the first time.
4. **Open it** — go to **http://localhost:8765** in your browser.

That's it. The dashboard you see *is* CloudlessOS: click an app to install and run it on
your GPU, pick a model, or just chat with your local AI.

**Want to reach it from your phone or another laptop on the same Wi-Fi?** Start it on all
network interfaces instead:
```bash
CLOUDLESS_ADDR=0.0.0.0:8765 go run ./cmd/cloudlessd
```
then open `http://<this-machine-ip>:8765` from the other device. Only do this on a network
you trust — it exposes the dashboard to everyone on that network.

Stuck, or on Windows? The full setup, requirements and known gotchas are in
[`docs/DEV_ENVIRONMENT.md`](./docs/DEV_ENVIRONMENT.md), and the daemon's own notes are in
[`orchestrator/README.md`](./orchestrator/README.md).

## Build the development ISO

The Ubuntu 24.04 installer pipeline, Debian packages, systemd services, kiosk session,
Plymouth branding, and USB instructions live in [`distro/`](./distro). Start with
[`distro/README.md`](./distro/README.md).

## Repository

- Start here: [`CLAUDE.md`](./CLAUDE.md) — project context overview.
- The working prototype: [`orchestrator/`](./orchestrator) — the `cloudlessd` daemon + web UI.
- Recipe discovery and signed catalog service: [`services/recipe-indexer/`](./services/recipe-indexer)
- Detailed docs: [`docs/`](./docs)
  - [Vision](./docs/VISION.md)
  - [Architecture](./docs/ARCHITECTURE.md)
  - [Roadmap](./docs/ROADMAP.md)
  - [Decisions](./docs/DECISIONS.md)
  - [Dev environment](./docs/DEV_ENVIRONMENT.md)
  - [Live status](./docs/STATUS.md)

## License

TBD — see open question on open-sourcing the OS in [`docs/DECISIONS.md`](./docs/DECISIONS.md).
