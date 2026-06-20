# Live Status

> The cold-start anchor. If resuming work, read this first (after `CLAUDE.md`).
> Keep it current — update the date and sections whenever state changes.

**Last updated:** 2026-06-19

## Where we are

In **Phase 0 (orchestrator prototype)** with a working, progressively-improving slice. The
Go daemon in `orchestrator/` installs/runs/stops AI apps as GPU containers and serves a web
UI. Installs are **asynchronous with live progress** (Server-Sent Events). Verified
end-to-end: Ollama pulled with streamed layer progress, ran as a GPU container, stopped +
removed via the API.

## Done

- Defined vision, architecture (3 layers + thin kiosk shell), roadmap, and decisions.
- Initialized git repo at `D:\Cloudless` with documentation structure.
- Dev box fully set up: Windows 11 + RTX 5090, WSL2 + Ubuntu 24.04, Docker 29.6.0 +
  NVIDIA Container Toolkit 1.19.1, Go 1.26.4. Container GPU access verified.
- Chose Go for the orchestrator (D6); stdlib-only, Docker via CLI behind an interface.
- Built the orchestrator (`cloudlessd`): engine abstraction + Docker impl, catalog
  (Ollama/Open WebUI/ComfyUI), HTTP API, embedded web UI. Builds + vets clean.
- Smoke test passed (`scripts/smoke-test.sh`): full Ollama lifecycle on GPU.
- Async install jobs + SSE progress streaming (`internal/jobs`, `/api/jobs/...`):
  `start` returns a jobId immediately; UI streams live per-layer pull progress.
- **UI redesign + first-run onboarding** (`internal/api/web/`): glassmorphism over a
  Three.js particle background (vendored offline, D7), top-bar GPU/health status, time-based
  greeting, redesigned app cards, and a 3-step welcome (Welcome → GPU detection → guided
  first install). Verified served correctly (index + `/vendor/three.min.js` 200).
- **Server-side first-run state** (`internal/state`, D8): the daemon — not the browser —
  decides first launch (absence of a per-user state file). `GET /api/onboarding` +
  `POST /api/onboarding/complete`. Verified it persists across daemon restarts.
- **OS home screen**: backends `internal/hardware` (multi-GPU stats → `gpus[]` from
  `GET /api/gpu`) and `internal/places` (folders under `~/Cloudless` → `GET /api/folders`,
  `POST /api/folders/{id}/open`).
- **macOS-style redesign** (D10, supersedes D9's blocky take): translucent menu bar,
  centered "Welcome to Cloudless" hero, frosted vibrancy cards (Graphics, Places),
  Launchpad-style app grid (large rounded icons, hover lift, running dot, hover-to-stop).
  Three.js removed in favor of a pure-CSS Big Sur gradient wallpaper. Red Hat Mono kept,
  used lightly. Verified assets/font 200 and GPU/folders payloads.

- **Pre-installed apps + Chat button** (`internal/provision`, D11): vLLM, Open WebUI,
  ComfyUI auto-provision on startup onto a shared `cloudless` network. Hero "Chat with
  your Cloudless AI" button opens Open WebUI (branded, no login).
- **vLLM is the default engine** (D12, supersedes Ollama default): Open WebUI repointed to
  vLLM's OpenAI API; Ollama demoted to optional. **Validated on Blackwell** — vLLM 0.23.0
  on the RTX 5090 served Qwen2.5-1.5B and returned a completion; Open WebUI lists the
  `cloudless` model over the network.
- **SGLang alternative engine** (D13): pre-fetched (image ready, ~41.6 GB) but not run by
  default — switch to it instead of vLLM. New `Prefetch` provisioning mode. Verified:
  provisioner fetches it without starting it.
- **OpenClaw + Hermes agents** (D14): no upstream image, so **built locally** from embedded
  Dockerfiles (new `apps` package + `engine.Build` + `building` install phase) and
  pre-wired to Cloudless AI. Verified: both build and run; OpenClaw logs
  `agent model: custom/cloudless`; daemon build-and-install path works via the API.
- **OpenClaw plug-and-play** (D16): no auth wall — gateway runs `--auth none --bind loopback`
  under host networking (reachable at `localhost:18789`, nothing to enter), reaching the
  active engine via host `127.0.0.1:8000`. Engine gained `host` networking support. Verified.
- **Smooth engine switching** (D15): all clients use one stable endpoint
  `http://cloudless-ai:8000/v1`; the active engine owns the `cloudless-ai` alias on fixed
  port 8000, serving model id `cloudless`. `GET/POST /api/engine`; engine pills in the UI.
  Choice persists; exactly one engine runs; self-healing alias. Verified vLLM↔SGLang
  round-trip survives restart; completion works through the stable endpoint after a switch.

## In progress

- Nothing actively mid-change. Ready to pick the next Phase 0 increment.

## Next steps (candidates, roughly prioritized)

1. **Validate ComfyUI on Blackwell (RTX 50xx)** — current image (mmartial/...) is pinned
   but unverified on sm_120; confirm or swap for a CUDA 12.8+/PyTorch-Blackwell build.
   (vLLM is already validated on Blackwell — D12.)
2. **Multi-GPU scaling** — use all GPUs for the *active* engine via tensor parallelism
   (vLLM `--tensor-parallel-size` / SGLang `--tp` from the detected GPU count). One engine
   at a time stays the invariant — no running two engines at once (D15). Validate on the
   3-GPU box (dev box is single-GPU).
3. **Model manager** — pick/switch the served model (one per engine instance today) and
   show "fits your VRAM".
3. **Agent UX polish** — OpenClaw/Hermes install + run pre-wired (D14); next is exposing
   their UIs/setup (messaging platforms, allowlists) cleanly in the launcher.
3. **State persistence for apps/jobs** — beyond `docker ps` + in-memory, likely extending
   `internal/state` (onboarding state already lives there, D8).

## Known limitations (see orchestrator/README.md)

- Pull progress is layer-level, not byte-level percentages (docker non-TTY output).
- Job + app state is in-memory / derived from `docker ps`; no persistence yet.
- ComfyUI recipe is a placeholder pending a validated image.

See `DEV_ENVIRONMENT.md` for setup/runbook and WSL gotchas.

## Decisions pending input

- Orchestrator language (Go / Python / Rust), web UI framework, Docker vs Podman.
- Final shipped-distro base (immutable Fedora-family vs Ubuntu).
- Open-source CloudlessOS? (leaning yes) + license.

## Blockers

- None. Waiting on the WSL2 Ubuntu install to proceed to GPU verification.
