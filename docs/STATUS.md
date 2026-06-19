# Live Status

> The cold-start anchor. If resuming work, read this first (after `CLAUDE.md`).
> Keep it current — update the date and sections whenever state changes.

**Last updated:** 2026-06-19

## Where we are

In **Phase 0 (orchestrator prototype)** with a working thin slice. The Go daemon in
`orchestrator/` installs/runs/stops AI apps as GPU containers and serves a web UI. Verified
end-to-end: Ollama pulled, ran as a GPU container, and was stopped + removed via the API.

## Done

- Defined vision, architecture (3 layers + thin kiosk shell), roadmap, and decisions.
- Initialized git repo at `D:\Cloudless` with documentation structure.
- Dev box fully set up: Windows 11 + RTX 5090, WSL2 + Ubuntu 24.04, Docker 29.6.0 +
  NVIDIA Container Toolkit 1.19.1, Go 1.26.4. Container GPU access verified.
- Chose Go for the orchestrator (D6); stdlib-only, Docker via CLI behind an interface.
- Built the orchestrator (`cloudlessd`): engine abstraction + Docker impl, catalog
  (Ollama/Open WebUI/ComfyUI), HTTP API, embedded web UI. Builds + vets clean.
- **Smoke test passed** (`scripts/smoke-test.sh`): full Ollama lifecycle on GPU.

## In progress

- Nothing actively mid-change. Ready to pick the next Phase 0 increment.

## Next steps (candidates, roughly prioritized)

1. **Async install jobs + progress streaming** — replace the blocking synchronous pull
   so the UI shows live download/extract progress (the biggest UX gap right now).
2. **ComfyUI recipe** — pin a validated image so the image-gen app actually works.
3. **Model manager v0** — download models + "fits your VRAM" recommendations.
4. **State persistence** — track installed apps beyond `docker ps`.
5. **UI polish + onboarding** — first-run flow.

## Known limitations (see orchestrator/README.md)

- `start` pulls synchronously (blocks minutes on first run).
- App state derived from `docker ps`; no separate persistence yet.
- ComfyUI recipe is a placeholder pending a validated image.

See `DEV_ENVIRONMENT.md` for setup/runbook and WSL gotchas.

## Decisions pending input

- Orchestrator language (Go / Python / Rust), web UI framework, Docker vs Podman.
- Final shipped-distro base (immutable Fedora-family vs Ubuntu).
- Open-source CloudlessOS? (leaning yes) + license.

## Blockers

- None. Waiting on the WSL2 Ubuntu install to proceed to GPU verification.
