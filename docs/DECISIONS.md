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
Third-party assets are **vendored locally** (Red Hat Mono woff2 at
`internal/api/web/vendor/fonts/`), never loaded from a CDN. (Three.js was vendored here
too, then removed in D10 when the background became pure CSS.)

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
**Date:** 2026-06-20 · **Status:** Visual language superseded by D10; backends still current

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

## D10 — Visual language: macOS-style (elegant, soft, Launchpad), not blocky
**Date:** 2026-06-20 · **Status:** Accepted (supersedes D9's blocky aesthetic)

The home screen targets a **macOS (Big Sur-era) feel**: simple, elegant, intuitive.
Concretely: a thin translucent **menu bar**; a centered **"Welcome to Cloudless"** hero;
**frosted "vibrancy" cards** (translucent, blurred, rounded ~18px, soft shadows, hairline
edges — not hard borders) for Graphics and Places; and a **Launchpad-style app grid** of
large rounded icons with per-app gradients, hover lift, a running dot, and a hover stop
badge. Background is a **pure-CSS Big Sur gradient** with slow drifting blurred blobs —
**Three.js was removed** (the particle field read as "techy", not elegant; CSS is lighter
and offline by default). Font stays Red Hat Mono (used more lightly). Palette uses macOS
system blue `#0a84ff`.

**Why:** User direction — "very macOS X, simple, elegant, intuitive." The earlier blocky
take (D9) felt too hard/techy and the launcher didn't read as a launcher. The `hardware`
and `places` backends from D9 are unchanged.

---

## D11 — Bundled apps are pre-installed on first boot; Open WebUI is "Cloudless AI"
**Date:** 2026-06-20 · **Status:** Accepted (engine/default-model choice superseded by D12)

Ollama, Open WebUI, and ComfyUI ship **pre-installed**: `internal/provision` auto-pulls
and runs them on daemon startup (background goroutine, best-effort, idempotent — skips
already-running, logs failures, never blocks). They join a shared docker network
(`cloudless`) so they resolve each other by name. Open WebUI is branded and wired via env:
`WEBUI_NAME="Cloudless AI"`, `WEBUI_AUTH=False` (no login on a local appliance),
`OLLAMA_BASE_URL=http://cloudless-ollama:11434`. A default chat model (`llama3.2:1b`,
override with `CLOUDLESS_DEFAULT_MODEL`, "" to skip) is pulled into Ollama so chat works
out of the box. The home screen has a prominent **"Chat with your Cloudless AI"** button
that opens Open WebUI (enabled once it's running).

**ComfyUI caveat:** pinned to `mmartial/comfyui-nvidia-docker:latest` (community image) and
marked `Verified:false`. It is **not yet validated on Blackwell (RTX 50xx)** — older CUDA
builds won't run on sm_120, so the image may need swapping. Provisioning it is best-effort;
failure doesn't affect the rest.

**Verified:** provisioner brings up Ollama + Open WebUI on the `cloudless` network with
correct branding/wiring; Open WebUI reaches Ollama by DNS ("Ollama is running").

---

## D12 — Default inference engine is vLLM, not Ollama
**Date:** 2026-06-20 · **Status:** Accepted (supersedes the Ollama default in D11)

**Cloudless AI is backed by vLLM**, not Ollama. Ollama is a convenience wrapper over
llama.cpp and isn't the performance choice; vLLM (PagedAttention + continuous batching,
optimized CUDA kernels) gives far higher throughput/concurrency — the right default for
the capable/multi-GPU machines Cloudless targets.

Shape:
- vLLM runs as a **Service** app (hidden from the launcher): OpenAI-compatible server on
  `:8000`, image `vllm/vllm-openai:latest` (entrypoint `vllm serve`, so the model is the
  **positional** arg). Serves the model under the name `cloudless`. Default model
  `Qwen/Qwen2.5-1.5B-Instruct`, override via `CLOUDLESS_DEFAULT_MODEL` (any HF id). Model
  cache persists in the `cloudless-hf` named volume. `--gpu-memory-utilization 0.5` leaves
  headroom on a shared desktop GPU.
- **Open WebUI repointed** to the OpenAI API: `ENABLE_OLLAMA_API=False`,
  `OPENAI_API_BASE_URL=http://cloudless-vllm:8000/v1`, `OPENAI_API_KEY` placeholder.
- **Ollama kept** as an optional, non-default Service app (some users may still want it).
- Engine gained `RunSpec.Args` (container command); catalog gained `Command`, `Volumes`,
  `Service`.

**Validated on Blackwell (the open risk):** vLLM **0.23.0** runs on the **RTX 5090
(sm_120)** — it served Qwen2.5-1.5B and returned a real completion; Open WebUI reaches it
by container DNS and lists the `cloudless` model. So the Blackwell concern that applies to
ComfyUI (D11) does **not** block vLLM.

**Tradeoffs:** vLLM uses more VRAM, is GPU-only, and its model management is less turnkey
than `ollama pull` (one model per server instance; switching = restart). A future model
manager / engine-switching UI should address this. For single-user low-VRAM cases,
llama.cpp remains the better fit and is a candidate second engine.

---

## D13 — SGLang as a pre-fetched alternative engine; OpenClaw/Hermes agents pending
**Date:** 2026-06-20 · **Status:** Accepted (SGLang); agents now implemented in D14

**SGLang** (`lmsysorg/sglang:latest`) is added as an alternative inference engine. It's
**pre-fetched** (image pulled on boot) but **not run by default** — running two engines
would contend for VRAM, so it's "installed and ready to switch to" rather than active. New
`Prefetch` flag + `catalog.Bundled()`: Preinstall apps are pulled *and* run; Prefetch apps
are pulled only. Launch command passes the model positionally via `--model-path` (image
entrypoint is the NVIDIA wrapper), OpenAI-compatible on `:30000`. **Note:** the SGLang
image is ~41.6 GB — a meaningful disk cost to pre-install; revisit a slimmer tag.

**OpenClaw and Hermes** are AI *agents* (OpenClaw: local agent gateway that actions your
machine; Hermes: Nous Research self-improving agent). Both connect to an OpenAI-compatible
LLM, so wiring them to Cloudless AI (`http://cloudless-vllm:8000/v1`) is just a base-URL
setting. **But neither ships an official Docker image** — they're installer/desktop-based
(OpenClaw `curl install.sh` / repo `moltbot/moltbot`; Hermes curl installer + desktop GUI,
config in `~/.hermes/config.yaml`), and exact config keys aren't documented in sources
found. They're added as **recipe-pending** catalog entries (`Image:""`, shown as "coming
soon" in the launcher) rather than shipping guessed/broken containers.

**Open decision:** how to package the agents — (a) custom Dockerfiles that run their
installers and bake in the Cloudless AI config, or (b) host-level install (they're really
desktop/host apps). Needs validated image + config keys before implementing.

---

## D14 — Agent apps are built locally from embedded Dockerfiles, pre-wired to Cloudless AI
**Date:** 2026-06-20 · **Status:** Accepted

OpenClaw and Hermes have no upstream Docker images, so Cloudless **builds them locally**.
`internal/apps/<name>/` holds an embedded Dockerfile + baked config (go:embed); the engine
gains `Build(image, contextDir)` and `apps.Materialize` writes the embedded context to a
temp dir; catalog apps with a `Build` field are **built instead of pulled** by the install
flow (new job phase `building`). On first install the launcher builds, then runs.

- **OpenClaw** (`node:24-slim` + `npm i -g openclaw`): baked `~/.openclaw/openclaw.json`
  declares a `custom` provider → `http://cloudless-vllm:8000/v1`, model `custom/cloudless`,
  `gateway.mode=local`, and `OPENCLAW_GATEWAY_TOKEN` (gateway refuses 0.0.0.0 without auth).
  Provider `models` must be an array of objects (`[{id,name}]`). Runs the gateway on `:18789`.
- **Hermes** (`debian` + official curl installer; needs `xz-utils` for its Node download):
  baked `~/.hermes/config.yaml` (`provider: custom`, `base_url: …vllm…/v1`) + `.env`
  `OPENAI_API_KEY`. Runs `hermes gateway run` (foreground; `start` needs systemd).

**Verified:** both images build and run; OpenClaw logs `agent model: custom/cloudless`.
The daemon install path (build from the *embedded* context via `POST /api/apps/openclaw/start`)
brings the container up wired to Cloudless AI.

**Caveats:** the LLM backend is pre-wired, but each agent still needs user-specific setup
to be fully useful (OpenClaw: connect messaging platforms; Hermes: user allowlists /
platforms). Config schemas were reverse-engineered from docs + iterated against the real
binaries, so they may need updates as these fast-moving tools change.

---

## D15 — Smooth engine switching via a stable endpoint
**Date:** 2026-06-20 · **Status:** Accepted

**Problem:** clients (Open WebUI, OpenClaw, Hermes) baked the engine URL at container
creation pointing straight at `cloudless-vllm:8000`, so switching engines would break them.

**Solution — one stable endpoint that the active engine "owns":**
- Every engine listens on the **fixed port 8000** and, when active, carries the docker
  **network alias `cloudless-ai`** (`RunSpec.NetworkAlias`; catalog `Engine` apps get it in
  `Spec()`). Both engines serve the model id **`cloudless`** (`--served-model-name`), so the
  endpoint *and* the model name are identical across engines — clients can't tell.
- All clients are configured **once** to `http://cloudless-ai:8000/v1` and never touched again.
- API: `GET /api/engine` (active / ready / list), `POST /api/engine/{id}` (async job:
  stop current → start chosen → poll `/v1/models` until ready). UI: engine pills in the
  Graphics card; the Chat button is gated on engine readiness.
- Single GPU ⇒ one engine at a time. A switch is stop-then-start, so there's a model-load
  gap (~50 s for SGLang) surfaced in the UI; clients reconnect automatically via the stable
  name. The choice is **persisted** (`state.Engine`); on boot the provisioner runs only the
  selected engine (stopping others *first* to free the port) and self-heals a missing alias
  (`HasAlias`). Both engine images are still pulled so either is ready instantly.

**Verified:** `cloudless-ai` follows the active engine from inside Open WebUI; vLLM↔SGLang
round-trips; the selection survives a daemon restart with exactly one engine running; a
completion works through `cloudless-ai` after switching. Both engines run on Blackwell (sm_120).

**One engine at a time — invariant (all machines).** Even on multi-GPU Cloudless PCs we run
**exactly one** inference engine at a time: a single port (`:8000`), a single `cloudless-ai`
alias, never two engines concurrently. Product decision (2026-06-20): don't run vLLM and
SGLang side by side. So a switch always incurs the model-load downtime — accepted by design.

**Multi-GPU is for scaling the *active* engine, not concurrency.** Multiple GPUs make the one
running engine bigger/faster via tensor parallelism (vLLM `--tensor-parallel-size N`, SGLang
`--tp N`). **Planned enhancement:** set the TP size from `hardware.GPUs()` count so the active
engine uses all GPUs (today both engines default to one GPU). Not yet built; validate on the
3-GPU machine (dev box is single-GPU).

---

## D16 — OpenClaw is plug-and-play: no auth, host networking
**Date:** 2026-06-20 · **Status:** Accepted

**Problem:** OpenClaw's gateway **refuses to bind `0.0.0.0` without auth**, so on a normal
container (`-p 127.0.0.1:18789`) it demanded a gateway token/password — an auth wall, not
plug-and-play.

**Solution:** run the gateway `--auth none --bind loopback` under **host networking**. The
no-auth guard only blocks *non-loopback* binds, so a loopback bind is accepted with no auth;
host networking puts that `127.0.0.1:18789` on the **host's** loopback, reachable by the
browser with nothing to enter. With host networking it reaches the active engine via the
host-published `127.0.0.1:8000` (which already follows engine switches, D15), so OpenClaw's
baked `baseUrl` is `http://127.0.0.1:8000/v1`. The engine gained host-networking support
(`Network:"host"` → `--network host`, skip `-p` and the alias).

**Why no auth is fine:** it's a local single-user appliance only ever published to
localhost — same stance as Open WebUI (`WEBUI_AUTH=False`) and vLLM (no api-key).

**Verified:** gateway logs `auth mode=none` (no "Refusing"), `agent model: custom/cloudless`,
reachable at `localhost:18789`; the daemon build-and-install path brings it up the same way.

**Follow-up:** review Hermes for a similar wall (it starts without blocking, but its
UI/access may want the same no-auth pass).

---

## Open questions (not yet decided)

- **Open-source CloudlessOS?** Leaning yes (trust/community for a privacy brand, like
  Pop!_OS). License TBD.
- **Orchestrator language:** Go vs Python vs Rust.
- **Web UI framework.**
- **Catalog scope:** how curated vs how extensible (defensibility = curation + reliability).
