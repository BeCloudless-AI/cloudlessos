# Live Status

> The cold-start anchor. If resuming work, read this first (after `CLAUDE.md`).
> Keep it current — update the date and sections whenever state changes.

**Last updated:** 2026-06-20

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
- **Tool calling enabled** (D17): both engines launch with the Qwen2.5 tool-call parser
  (vLLM `--enable-auto-tool-choice --tool-call-parser hermes`, SGLang `--tool-call-parser
  qwen25`) so agents (OpenClaw) work — was failing with "provider rejected the request
  schema". Also fixed a provisioner↔switch race (`provision.EngineMu`). Verified on both.
- **Smooth engine switching** (D15): all clients use one stable endpoint
  `http://cloudless-ai:8000/v1`; the active engine owns the `cloudless-ai` alias on fixed
  port 8000, serving model id `cloudless`. `GET/POST /api/engine`; engine pills in the UI.
  Choice persists; exactly one engine runs; self-healing alias. Verified vLLM↔SGLang
  round-trip survives restart; completion works through the stable endpoint after a switch.

- **Settings panel** (D18): gear → frosted sheet with Cloudless AI (engine switch +
  editable model, applied by restarting the engine), Apps (per-app **Reset**/Uninstall —
  the "fix a mistake" button), System (replay welcome). Runtime-configurable model
  (`state.Model`). Verified: settings served, OpenClaw reset rebuilds + restarts cleanly.
- **Per-app config** (D19/D21): config moved from baked-in to **mounted** editable files;
  edited via an intuitive **form** (inputs, password fields, toggles) mapped to JSON
  dot-paths (OpenClaw) and `.env` keys (Hermes), plus a collapsible Advanced raw editor.
- **Settings is a multi-page app** (D22): two-pane (sidebar + content) like macOS System
  Settings — pages for Cloudless AI (engine + model), Hardware (GPU + folders), each App
  (status/Open + form config + Advanced + Reset/Uninstall), and General. Verified: served,
  markers present, JS passes `node --check`.

- **Flat Swiss/instrument restyle** (D20, supersedes D10): adapted to a user reference —
  flat (no glass/blur/shadow), light-grey base with white hairline tiles, **monochrome +
  sky-blue (brand/interactive) + orange (live status)**, Red Hat Mono with a big numeral
  clock and tiny lowercase labels. Verified served; visual tuning is a follow-up.

- **Dashboard rework** (D23, refines D20/D22): engine choice removed from the home screen
  (lives only in Settings → Cloudless AI now); Open WebUI hidden from the launcher (new
  `App.Hidden` catalog flag — reached via the Chat button instead); clock shows **seconds**;
  **theme follows the hour** (dawn/day/dusk/night via `html[data-theme]`, keeping blue+orange);
  a quiet **three.js** point-field background re-added (`vendor/three.min.js` r149, theme-tinted,
  WebGL-optional). Fixed an undefined `--line` CSS var. Verified built/served/checked; visual
  pass still pending (headless box).
- **Motion / "feels alive" pass** (D23 follow-up): organic progress fills (flowing gradient +
  sheen + glow; GPU bars glide to new values in place), a real per-tile install progress bar
  (determinate from layer counts, indeterminate barber-pole otherwise), springy hovers,
  breathing status indicators, a pulsing Chat CTA, and a one-time staggered entrance — all
  gated by `prefers-reduced-motion`. Built/served/checked; visual pass still pending.
- **Cloudless Assistant** (D24, `internal/assistant`): a built-in chat (floating FAB → panel)
  that knows the OS, the company goal, the live machine state and the app catalog, and runs on
  the **local engine** (`POST /api/assistant/chat`, SSE streaming). Replies can carry one-click
  actions (install/open/chat/switch-engine). Verified end-to-end against the live engine.
- **App Launcher + fast-launch pins** (D25): dashboard now shows a curated **Fast launch** of
  *pinned* apps; the full categorized set lives in a **full-screen App Launcher** (search,
  per-app description + Install/Open/Stop/Configure/Pin). New catalog `Category`/`Tagline`;
  pins persist (`/api/pins`, `/api/apps/{id}/pin`), default empty. `CLOUDLESS_NO_PROVISION=1`
  added for safe side-by-side test daemons. Built/served/checked; visual pass pending.
- **Assistant-first hero + per-app launcher pages** (D26): the hero now leads with an assistant
  **prompt bar** (replacing the "Chat with your Cloudless AI" button; full chat demoted to a
  small secondary link). Launcher cards open a **per-app page** with a long description +
  "examples of what you can do" (new catalog `Long`/`Examples`). Built/served/checked.
- **Networking — share online + local network** (D27/D28): per-app **"share online"** toggle
  (Cloudflare quick tunnel via cloudflared sidecar → public `trycloudflare.com` URL) and
  **"local network"** serving (host-networked socat sidecar bound to `<LAN-IP>:<port>` → reach
  apps from other devices at the same `http://IP:port`). A machine-wide **Local network** default
  (ON) is chosen in a new **welcome-tour step** and applied to all web apps (`/api/network/local`,
  `provision.EnsureLAN`, re-applied on boot). Tunnel verified live (HTTP 200 round-trip);
  cloudflared confirmed working under WSL — LAN reachability is the only WSL-limited part.
- **Connectivity indicator** (D29): menubar chip + popover showing internet / local-network /
  offline with a plain-language explanation (`GET /api/network/status`, parallel TCP probes,
  cached). Offline shown as neutral, not an error.
- **App updates + persistence** (D30): every app's mutable state is now volume-backed (added
  open-webui `/app/backend/data`, comfyui `/comfy/mnt`) so updates don't reset data. Per-app
  **update check** (local vs remote image digest) + **Update now** (pull latest → recreate on
  same volumes) in each app's Settings; Cloudless-validated-manifest model (catalog tags for now).
  Verified against live images. OS-image (bootc) OTA still pending.
- **Ollama removed** (D31): dropped from the catalog/UI entirely (leftover from the vLLM/SGLang
  switch). Catalog: vllm, sglang, open-webui, comfyui, ai-toolkit, unsloth, openclaw, hermes.

## In progress

- Nothing actively mid-change. Ready to pick the next Phase 0 increment. **Eyes-on visual
  pass owed** on the D20/D23 restyle (can't render on the headless dev box).

## Next steps (candidates, roughly prioritized)

1. **Validate ComfyUI + the trainers on Blackwell (RTX 50xx)** — ComfyUI (mmartial/...) plus the
   new **Training & fine-tuning** apps — **AI Toolkit** (`ostris/aitoolkit:latest`, image-model
   LoRA trainer, UI :8675) and **Unsloth** (`unsloth/unsloth:latest`, fast LLM fine-tuning, Jupyter
   Lab :8888) — are all pinned but unverified on sm_120; confirm or swap for CUDA 12.8+/PyTorch-
   Blackwell builds. (vLLM is already validated on Blackwell — D12.)
2. **Multi-GPU scaling** — use all GPUs for the *active* engine via tensor parallelism
   (vLLM `--tensor-parallel-size` / SGLang `--tp` from the detected GPU count). One engine
   at a time stays the invariant — no running two engines at once (D15). Validate on the
   3-GPU box (dev box is single-GPU).
3. **Model manager** — ✅ v1 done (D32): curated non-gated catalog (`internal/models`),
   `GET /api/models` with a "fits your VRAM" verdict, a model-browser UI on the Cloudless AI
   page, one-click switch (restart-on-serve). Follow-ups: downloaded badge, pre-download,
   multi-backend (llama.cpp/MLX), per-model advanced config.
3. **Agent UX polish** — OpenClaw/Hermes install + run pre-wired (D14); next is exposing
   their UIs/setup (messaging platforms, allowlists) cleanly in the launcher.
3. **State persistence for apps/jobs** — beyond `docker ps` + in-memory, likely extending
   `internal/state` (onboarding state already lives there, D8).

## Known limitations (see orchestrator/README.md)

- Pull progress is layer-level, not byte-level percentages (docker non-TTY output).
- Job + app state is in-memory / derived from `docker ps`; no persistence yet.
- ComfyUI recipe is a placeholder pending a validated image.

See [[DEV_ENVIRONMENT]] for setup/runbook and WSL gotchas.

## Decisions pending input

- Orchestrator language (Go / Python / Rust), web UI framework, Docker vs Podman.
- Final shipped-distro base (immutable Fedora-family vs Ubuntu).
- Open-source CloudlessOS? (leaning yes) + license.

## See also

- [[ROADMAP]] — the phased plan this status tracks against
- [[DECISIONS]] — decision log (ADR-style) & open questions
- [[DEV_ENVIRONMENT]] — hardware, WSL2 setup & runbook

## Blockers

- None. Waiting on the WSL2 Ubuntu install to proceed to GPU verification.
