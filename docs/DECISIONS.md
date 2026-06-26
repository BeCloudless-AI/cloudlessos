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

**Why:** Avoid mistaking the easy 5% for the product. See [[ARCHITECTURE]].

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
**Date:** 2026-06-20 · **Status:** Superseded by D20 (flat Swiss/instrument look)

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

## D17 — Engines run with tool calling enabled (agents need it)
**Date:** 2026-06-20 · **Status:** Accepted

**Problem:** OpenClaw (an agent) sends `tools` + `tool_choice: auto`, and vLLM/SGLang
rejected it ("provider rejected the request schema or tool payload" → vLLM:
`"auto" tool choice requires --enable-auto-tool-choice and --tool-call-parser`). Agents are
unusable without tool calling.

**Fix:** launch both engines with the tool-call parser for the served model (Qwen2.5):
- vLLM: `--enable-auto-tool-choice --tool-call-parser hermes`
- SGLang: `--tool-call-parser qwen25`
(Both parser values verified valid for this Qwen2.5 model.) **Verified:** both engines now
return a valid `tool_calls` response (`get_weather({"location":"Paris"})`).

**Also fixed (found while verifying):** a **provisioner↔switch race** — an engine switch
fired during first-boot provisioning could be clobbered by the provisioner's stale "desired"
read (it would stop the just-started engine). Now both serialize on `provision.EngineMu` and
the provisioner re-reads the desired engine under the lock. Keeps switching smooth (D15).

**Context length:** engines serve the model's full **32K** context
(vLLM `--max-model-len 32768`, SGLang `--context-length 32768`) — agents send big prompts
(system + tool schemas + history) and overflowed the earlier 8K cap. Verified vLLM serves
`max_model_len: 32768` with KV cache fitting in VRAM (~11.9 GiB, ~13× concurrency).

**Note:** a 1.5B model does tool calling but is weak at agentic work; a stronger default
model (e.g. Qwen2.5-7B-Instruct — fits easily on a 32 GB GPU) would materially improve
OpenClaw/Hermes quality. Ties into the model-manager work.

---

## D18 — Settings panel (config + reset)
**Date:** 2026-06-20 · **Status:** Accepted

A gear in the menu bar opens a frosted **Settings sheet** (macOS System-Settings style,
reusing the overlay/card styling). Three sections:
- **Cloudless AI** — engine switch pills + an editable **Model** field (HF id) with Apply,
  which restarts the active engine on the new model. The model is runtime-configurable and
  persisted (`state.Model`); engines get it via `catalog.EngineSpec(app, model)`, which
  substitutes the chosen model for the default in the launch args (copying the slice so the
  shared catalog command isn't mutated).
- **Apps** — per-app **Reset** and **Uninstall**. Reset = remove container (+ image for
  locally-built apps so the recipe rebuilds) then reinstall — the "I messed up OpenClaw, fix
  it" button. Uninstall = remove container + image.
- **System** — Replay the welcome tour (resets onboarding).

Endpoints: `GET /api/settings`, `POST /api/settings/model`, `POST /api/apps/{id}/reset`
(async job), `POST /api/apps/{id}/uninstall`, `POST /api/onboarding/reset`; engine gains
`RemoveImage`. The switch path was refactored into `applyEngine` so model-change reuses it.

**Verified:** settings served + `/api/settings` correct; onboarding reset works; resetting
OpenClaw rebuilt and restarted it cleanly (`auth mode=none`, running).

---

## D19 — Per-app config is editable (mounted, not baked)
**Date:** 2026-06-20 · **Status:** Accepted

To make OpenClaw/Hermes "fully configurable," their config stops being baked into the image
and instead lives as **editable files in the state dir** (`<stateDir>/apps/<id>/<file>`),
seeded from the embedded defaults and **mounted into the container** (overriding the image's
copy). So a config change applies by **restarting** the app — no rebuild.

- Catalog apps declare `Config []ConfigFile{File, Path, Lang}` (exposed in `/api/catalog`).
  OpenClaw: `openclaw.json`; Hermes: `config.yaml` + `.env`.
- `Server.appSpec(app)` = `app.Spec()` + the seeded config mounts; `runInstall`/reset use it.
- Endpoints: `GET /api/apps/{id}/config` (files + content), `POST /api/apps/{id}/config`
  (write + restart), `POST /api/apps/{id}/config/reset` (restore defaults + restart).
- Settings → Apps → **Configure** opens a raw editor (one textarea per file, monospace) with
  Save & apply and Reset-to-defaults. The whole config file is editable = "literally
  everything." App-level **Reset** (rebuild image/container) is separate and keeps user
  config; **Reset to defaults** (in Configure) restores it.

**Verified:** editing `openclaw.json` via the API lands in the container's mounted file and
OpenClaw still starts (`auth mode=none`).

---

## D20 — Visual language: flat, monochrome + sky-blue & orange (Swiss/instrument)
**Date:** 2026-06-20 · **Status:** Accepted (supersedes D10's macOS look)

Adapted to a user-provided reference (instrument-panel / editorial / Swiss). **Flat, not
glassy** — no blur, no soft shadows: a solid light-grey background (`#e8e8e7`) with white,
hairline-bordered tiles (1px `rgba(0,0,0,.1)`), crisp and high-contrast.

**Two-accent system on a monochrome base** (keeping the cloudless sky blue per the brand):
- **Blue `#1f6bff` (cloudless sky) = brand/interactive** — logo mark, primary buttons, the
  chat CTA, active engine pill, GPU/data bars, focus borders, selected states.
- **Orange `#ff5a00` = live status** — the hero accent dot, status LEDs, the running dot.
- Black/white/grey carry everything else.

Type: **Red Hat Mono** (D9) with **oversized bold numerals** (a big `HH:MM` clock as the hero
focal point, like the reference) and **tiny lowercase labels** (`graphics`, `places`,
`engine`, GPU stat rows). App icons are flat neutral tiles (the per-app gradient backgrounds
were removed). GPU stats render as bordered chip tiles with bold values.

**Verified:** page + font serve 200; markers present; **zero `backdrop-filter`** remain.
(Visual rendering not seen — headless — so spacing/scale is a follow-up tuning pass.)

---

## D21 — App config is a form, not JSON (raw editor kept as "Advanced")
**Date:** 2026-06-20 · **Status:** Accepted (refines D19)

The per-app config UI (D19) was a raw textarea — wrong for an OS. Replaced with intuitive
**form controls**. Catalog apps declare `Settings []Field{Key, Label, Help, Type, Options,
Default, File, Path}`; types are **text / password / number / toggle / select**. Each field
maps to a location in a config file, applied with **stdlib only**:
- **JSON dot-path** get/set for json config (OpenClaw's `openclaw.json` was rewritten to
  strict JSON so `encoding/json` can round-trip it; path keys may contain `/`, e.g.
  `agents.defaults.models.custom/cloudless.alias`).
- **`KEY=VALUE` line edit** for env config (Hermes `.env`), preserving other keys.

`GET /api/apps/{id}/settings` returns the schema + current values; `POST` writes the values
into the mounted config files and restarts the app. A collapsible **Advanced — raw config**
keeps the full file editor (D19) for power users.

Fields today: OpenClaw (AI endpoint, model name); Hermes (allow-all toggle, Telegram/Discord
tokens, allowed users) — extensible by adding `Field`s. **Verified:** toggle/password/text
save to `.env`; text saves to the `openclaw.json` path; both restart cleanly; form served.

---

## D22 — Settings is a multi-page app (sidebar + content), not one sheet
**Date:** 2026-06-20 · **Status:** Accepted (refines D18)

The single scrolling settings sheet didn't feel OS-grade. Rebuilt as a two-pane **Settings
app** (macOS System-Settings style): a left **sidebar** of grouped pages and a right
**content pane** that swaps per page.

- **Sidebar groups:** Cloudless (Cloudless AI, Hardware) · Apps (one entry per installed
  app) · System (General). Active item highlighted in blue.
- **Pages:** *Cloudless AI* (engine switch + model) · *Hardware* (per-GPU stats as chip
  tiles + folder/places) · *each App* (running/stopped status + Open, the D21 form config,
  collapsible Advanced raw config, Reset & Uninstall) · *General* (replay welcome tour, about).
- Per-app config is now its **own page** (form), not an inline expander.

Frontend-only — reuses existing endpoints. **Verified:** serves; sidebar/nav/page markers
present; the page JavaScript passes `node --check`.

---

## D23 — Dashboard rework: live three.js field, hourly themes, engine moved off home
**Date:** 2026-06-20 · **Status:** Accepted (refines D20, D22)

Tightened the home screen to feel like a finished OS rather than a control panel.

- **Engine choice removed from the dashboard.** Switching engines now lives only in
  Settings → Cloudless AI (D22). The Graphics card is just GPU stats; the dashboard keeps a
  hidden `renderEngine()` that fetches `/api/engine` purely to gate the Chat button on
  readiness. (`#engine-row` markup + its CSS removed; `er-pill` styles kept for Settings.)
- **Open WebUI hidden from the launcher.** New catalog flag `App.Hidden`
  (`json:"hidden,omitempty"`), set on `open-webui`. Filtered out of the app grid and the
  onboarding picks — it's reached through the hero "Chat with your Cloudless AI" button, so a
  duplicate tile was redundant. Still installed/provisioned as normal.
- **Clock shows seconds** (`HH:MM:SS`), tabular-nums so it doesn't jiggle.
- **Theme follows the hour** (ties to the "cloudless sky"): `applyTheme(hour)` sets
  `html[data-theme]` → **dawn** (05–08, warm light), **day** (08–18, the bare `:root`
  default), **dusk** (18–21, warm dark), **night** (21–05, deep blue dark). Implemented purely
  by overriding CSS custom properties; brand blue + orange status accents persist in every
  theme. Fixed a latent bug: `--line` was used by Settings but never defined — now defined in
  `:root` and each theme. Control backgrounds switched from literal `#fff` to `var(--control)`
  so inputs/pills track the theme.
- **Subtle three.js background re-introduced** (had been dropped in D10/D20 for the flat look):
  a slow, breathing point-grid wave — `#bg` canvas, vendored `vendor/three.min.js` (r149,
  offline per D7). Colour/opacity come from the theme's `--sky` / `--sky-alpha` vars, so it
  warms at dawn/dusk and glows blue at night. Falls back to the flat base if WebGL is absent;
  pauses when the tab is hidden. Kept deliberately quiet to respect the Swiss/instrument
  aesthetic (D20).

- **Motion / "feels alive" pass** (follow-up to the above): progress fills are now
  **organic** — GPU bars use a flowing gradient + a sweeping sheen + a soft glow, and their
  values **glide** to new readings (widths updated in place via `updateGPUValues`, not a
  rebuild-jump-cut every 4 s). App installs gained a **real per-tile progress bar**:
  determinate from docker layer counts, an indeterminate barber-pole when the total is
  unknown / building / starting. Plus springy, overshoot easings on tile/place/switch hovers,
  breathing live indicators (status LED, running dot, hero beacon dot), a gently pulsing
  "Chat" CTA, and a one-time staggered entrance for hero → cards → apps. All looping motion
  is disabled under `prefers-reduced-motion`.

Frontend + one catalog field. **Verified:** builds/vets clean; `node --check` passes;
`three.min.js` 200; `#engine-row` absent; `#bg` present; `data-theme`/`getSeconds`/`--line`
markers present; new motion markers (`.tbar`, `updateGPUValues`, `@keyframes flow/sheen/
breathe/beacon/cta/indet/tile-in`, `prefers-reduced-motion`) present; exactly one
`"hidden":true` (open-webui) in `/api/catalog`. Visual rendering is unverified (headless dev
box) — needs an eyes-on pass.

---

## D24 — Cloudless Assistant: a built-in guide powered by the local engine
**Date:** 2026-06-20 · **Status:** Accepted

A floating chat assistant (bottom-right) that helps the user decide what to do with the OS.
It is **grounded, not generic**: a server-built system prompt (`internal/assistant`) tells the
model what CloudlessOS is, the company goal, the **live machine state** (active engine +
readiness, model, GPU summary, first-run/onboarded), and the **app catalog** with per-app
"what it's for" and install status. It then drives the **same local engine** that powers
everything else — `POST /api/assistant/chat` streams from `127.0.0.1:8000/v1/chat/completions`
(served model `cloudless`) back to the browser as SSE; the UI renders tokens live.

- **Privacy stays intact:** the assistant runs on the user's own engine — no cloud call.
- **Actionable, within OS logic:** the model may end a reply with tags
  (`[[do:install:comfyui]]`, `[[do:open:…]]`, `[[do:chat]]`, `[[do:engine:sglang]]`). The
  backend parses them — and falls back to keyword intent on the user's last message — into a
  small set of one-click `actions` returned on the final SSE event. The UI turns these into
  buttons (install an app, open it, open chat, switch engine). Robust even with the small
  default model.
- **Graceful when cold:** if the engine isn't ready yet, the endpoint returns a friendly
  "warming up" message instead of an error.
- Frontend: a pulsing FAB → chat panel with streaming bubbles, a typing indicator, starter
  intent chips ("use AI agents", "build images", "analyze images", "just chat"), and action
  buttons. **Verified** end-to-end against the live engine — a grounded reply streamed and
  recommended ComfyUI for "I want to build images".

---

## D25 — App Launcher (full-screen, by category) + dashboard "fast launch" pins
**Date:** 2026-06-20 · **Status:** Accepted (supersedes the D23 "hide Open WebUI" tweak)

The dashboard app grid became a **curated "Fast launch"** of *pinned* apps; the full set moved
into a proper **App Launcher** (full-screen overlay, Launchpad-style).

- **Launcher:** apps grouped by **category** (`Chat & interfaces`, `Image & video`,
  `Agents & automation` — new `App.Category` + `App.Tagline` in the catalog), each card with a
  description, a status badge (running / available / soon), and **actions**: Install / Open /
  Stop, **Configure** (deep-links to the app's Settings page), and a **Pin/Unpin** toggle.
  Includes search. Engines (vLLM/SGLang/Ollama) stay out — they're infrastructure managed in
  Settings, not launchable apps (`App.Launchable()` = `!Service`).
- **Fast launch = pins:** the dashboard shows only pinned apps (with a graceful empty state +
  "Open the App Launcher" CTA). Pins persist per-user (`state.Pinned` + `PinnedSet`;
  `GET /api/pins`, `POST /api/apps/{id}/pin` toggle). **Default = none** (user's choice — they
  said start empty); this is why Open WebUI / ComfyUI can come *back* to the dashboard by being
  pinned. The earlier `Hidden` flag now only suppresses Open WebUI from first-run *suggestions*.
- **Dev affordance:** `CLOUDLESS_NO_PROVISION=1` skips startup provisioning so a second daemon
  (separate `CLOUDLESS_ADDR`) can be run for testing without touching the primary daemon's
  containers.

**Verified:** builds/vets clean; `node --check` passes; category/tagline served; pins default
`[]`, pin→`["comfyui"]`→unpin→`[]`; engines reject pinning (404); launcher/assistant UI markers
present. Visual rendering unverified (headless box).

---

## D26 — Assistant becomes the primary entry; launcher gets per-app pages
**Date:** 2026-06-20 · **Status:** Accepted (refines D24, D25)

Two UX refinements after first use.

- **Assistant is now the hero, not a corner afterthought.** The big "Chat with your Cloudless
  AI" button is gone; the hero leads with a prominent **prompt bar** — *"What do you want to do
  with CloudlessOS?"* — that opens the assistant (and sends the typed question). The full Open
  WebUI chat demotes to a small secondary "Open the full chat ↗" link under it. The floating FAB
  stays for re-access. This puts the OS-aware guide (which can take actions) front-and-centre.
  **Update:** "the full chat" is the **assistant itself in a full-screen mode** (a large centered
  window over a dimmed backdrop, with an expand/collapse toggle) — *not* Open WebUI. The assistant
  is now framed as "the Cloudless AI chat you're talking to"; Open WebUI is just an optional,
  separate dedicated-chat app in the launcher. The old OWUI entry points (the `localhost:3000`
  link and the `[[do:chat]]` action) were removed — "just chat" is answered by the assistant
  inline, and Open WebUI is only suggested when explicitly asked for.
- **Launcher app cards open a real per-app page.** Clicking a card (or "Details ›") opens a
  full page inside the launcher: big icon, status, a **long description**, and an **"Examples of
  what you can do"** bullet list, with the same Install/Open/Stop/Configure/Pin actions. New
  catalog fields `App.Long` + `App.Examples` (populated for Open WebUI, ComfyUI, OpenClaw,
  Hermes). Search or "← All apps" returns to the category grid. Action buttons and the card
  body share one wiring path (`wireActions`).

- **Launcher → two-pane master/detail + flicker fix** (follow-up): the card-grid layouts read
  as cluttered. Final design is **two panes** (like macOS System Settings): a **left list** of
  apps grouped by category (searchable, live running-dot, active highlight) and a **right pane**
  that shows an **intro** by default ("Your local AI apps" + category blurbs) and swaps to the
  **selected app's full page** on click — icon, name, status, long description, "examples of
  what you can do", and the action set (Open/Stop, Install, Configure, Pin). Also fixed a
  **re-render bug**: the dashboard's 8 s `/api/apps` poll called `renderApps`, which
  unconditionally rebuilt the open launcher every cycle (flicker + scroll jump). Now gated by a
  state signature (`lpStateSig` = running set + pins + selected app + query) so it only rebuilds
  when something it shows actually changed.

  **Flicker — final fix (decouple, not gate):** the signature/gating approaches kept failing
  in practice. Replaced entirely: the 8 s `/api/apps` poll (`renderApps`) now **never** renders
  the launcher — that code path was deleted. The launcher is redrawn **only** by explicit user
  interaction (open / select an app / search / install-stop-pin actions), each of which calls
  `refreshLauncher()`. There is no timer-driven path that can touch the launcher DOM, so
  poll-driven flicker is structurally impossible rather than merely gated.

  **Same fix for Settings:** the 4 s poll called `renderEngine()`, which rebuilt the entire
  Settings panel (`renderSettings()`) when open — flickering it and wiping any form input mid-
  type every 4 s. `renderEngine()` now only updates engine state and never renders Settings;
  the panel is redrawn solely by user actions (open / nav click / engine switch / job completion).
  Same principle: no timer-driven path may rebuild a panel the user is interacting with.

**Verified:** builds/vets clean; `node --check` passes; `long`/`examples` served; hero `ask`
bar + `renderAppDetail`/`showAppPage`/`ac-cta`/`ac-pin`/`lpStateSig` markers present; old
`chat-btn` gone; assistant still streams a real reply. Visual rendering unverified (headless box).

---

## D27 — "Share online" via Cloudflare quick tunnels (zero-config)
**Date:** 2026-06-20 · **Status:** Accepted

Users can expose an app (e.g. Open WebUI) to the internet with a single toggle in its Settings
page. Chosen mechanism: **Cloudflare Quick Tunnels** (`cloudflared tunnel --url …`), which mint
a public `https://<random>.trycloudflare.com` URL with **no Cloudflare account, domain, DNS or
login** — the easiest possible path, matching the "network must be easy" requirement.

- **Runtime:** a `cloudflare/cloudflared` container per shared app (`cloudless-tunnel-<id>`),
  run with **host networking** pointed at the app's published host port
  (`--url http://localhost:<hostPort>`). Host netns means one approach works for every app,
  including host-networked ones (OpenClaw); cloudflared is outbound-only so no inbound ports.
- **API:** `GET /api/apps/{id}/tunnel` → `{supported, enabled, url}`; `POST` `{enable}` starts
  (pull → run → scrape the `trycloudflare.com` URL from container logs, up to ~60s) or stops
  (remove the container). New `engine.Logs`. Catalog gains `Tunnelable()` (= launchable + has a
  web port) and `TunnelName()`. Apps without a web port (Hermes) report `supported:false`.
- **UI:** a "share online" block on the app's Settings page — a toggle, then the live public
  URL with Copy/Open, plus a blunt warning: **anyone with the link can use it** (quick tunnels
  add no auth; Open WebUI ships with no login). Persists via the container's restart policy.
- **Limits (intentional, v1):** URL is random and changes if the tunnel restarts; no access
  control. **Named tunnels** (stable custom-domain URL, needs a Cloudflare token) are a later
  upgrade. The cloudflared image is pulled on first enable.

**Verified:** builds/vets clean; `node --check` passes; `tunnel` endpoints return correct
`supported` flags (Open WebUI/ComfyUI true, Hermes false) and UI markers present. Did **not**
exercise live-enable in tests (it would publicly expose the running app).

---

## D28 — "Local network" serving via a same-port socat sidecar
**Date:** 2026-06-20 · **Status:** Accepted (companion to D27)

Apps run bound to `127.0.0.1` (localhost-only) by default. A per-app **"local network"** toggle
(Settings page, above "share online") lets the user serve an app to other devices on the same
network — phones, tablets, laptops — with a clear explanation of the exact address to use.

- **Mechanism (chosen):** a tiny host-networked **socat** sidecar (`cloudless-lan-<id>`,
  `alpine/socat`) that listens on **`<LAN-IP>:<port>`** and forwards to `127.0.0.1:<port>`.
  Binding to the specific LAN IP (not `0.0.0.0`) means it uses the **same port** as localhost
  without colliding with the app's existing `127.0.0.1:<port>` binding — so the URL is just
  `http://<LAN-IP>:<port>`, no offset, no port juggling.
- **Why a sidecar (not rebinding the app):** zero changes to how app containers are created —
  no recreation, no threading a bind-address through provision/install/restart, no persisting a
  per-app bind preference. Same low-risk pattern as the D27 tunnel. Enable/disable = run/remove
  one container.
- **IP detection:** standard UDP-dial trick (`net.Dial("udp","8.8.8.8:80")` → local addr),
  with an interface-scan fallback for the first private IPv4.
- **API:** `GET /api/apps/{id}/lan` → `{supported, enabled, ip, port, url}`; `POST {enable}`.
  New catalog `HasWebPort()` (shared with `Tunnelable()`) + `LanName()`. Apps with no web port
  (Hermes) report `supported:false`.
- **UI:** a "local network" block that always explains *how it works and which `http://IP:port`
  to open from another device*; toggling on shows the live address with Copy/Open and a
  one-line how-to. Persists via the sidecar's restart policy.
- **WSL caveat:** on the dev box the detected IP is the **WSL2 VM** address (172.x) — reachable
  from the Windows host but not from other LAN devices without Windows mirrored-networking/port-
  proxy. On bare-metal CloudlessOS the same code yields the real LAN IP and works directly.

**Verified:** builds/vets clean; `node --check` passes; `lan` endpoint returns the right
`supported`/`ip`/`port` (Open WebUI true + detected IP; Hermes false); UI markers present. Live
enable not exercised in tests (it would expose the running app on the network).

**Machine-wide default + onboarding (follow-up):** the welcome tour gained a **"Local network"**
step (a toggle, **default ON**, with a plain-language explanation of what it means and that it
can be changed later). The choice is a machine-wide preference (`state.LocalNet`, defaults ON
until set; `GET/POST /api/network/local`) applied by `provision.EnsureLAN`, which starts/stops
the per-app socat forwarders for every running web app. It's re-applied on every boot inside
`provision.Run` (and the sidecars also persist via their restart policy), so the setting sticks.
Shared `provision.PrimaryLANIP` + `catalog.LanSidecarSpec` back both the per-app toggle and the
global preference. **Verified:** default `enabled:true`; toggling to `false` persists; onboarding
step/markers served.

---

## D29 — Connectivity status (internet / local-only / offline)
**Date:** 2026-06-20 · **Status:** Accepted

A menubar indicator shows whether the machine can reach the **internet**, only the **local
network**, or **nothing** — with a click-to-open popover explaining what each state means for
the user (what they can/can't do). It complements the LAN/online-share features by making it
obvious *why* a download or public link might be unavailable.

- **Detection (`GET /api/network/status` → `{state,internet,lan,ip}`):** internet = a TCP
  connect to any of `1.1.1.1:443 / 8.8.8.8:53 / 1.1.1.1:53` succeeds within 2s (run in
  parallel, first hit wins; no DNS dependency); LAN = `provision.PrimaryLANIP() != ""`. State =
  `internet` if online, else `local` if on a network, else `offline`. The internet check is
  cached ~12s so the 20s UI poll stays snappy and offline doesn't stall.
- **UI:** a menubar chip with a colour-coded dot — **blue = Online**, **orange = Local network**,
  **grey = Offline** — and a popover with a title + plain-language message (e.g. offline: "Your
  apps run only on this machine… everything stays fully private") plus the machine's LAN IP.
  Offline is shown as neutral grey, not an alarm — being air-gapped is a legitimate private-AI
  choice, not an error.

**Verified:** builds/vets clean; `node --check` passes; endpoint returns `state:"internet"` on
the connected dev box; UI markers served.

---

## D30 — OTA updates: validated-manifest + recreate-on-volumes; persistence groundwork
**Date:** 2026-06-20 · **Status:** Accepted; persistence + app-update both built (OS update pending)

How CloudlessOS and its apps update over the internet **without resetting user config/data**.

- **The non-destructive rule:** an app's mutable state must live in **volumes**, never the
  container's writable layer. Then "update" = pull new image → `stop`+`rm` the old container
  (this does NOT delete named volumes/bind mounts) → `run` the new image with the **same name,
  env, ports and volumes**. Data survives; the app runs its own in-place data migrations.
- **Persistence groundwork (done now):** audited every app and added data volumes where missing —
  **open-webui** `/app/backend/data` (chats/settings) and **comfyui** `/comfy/mnt` (install,
  models, outputs, custom nodes). Engines already persist the HF cache; the trainers (AI Toolkit,
  Unsloth) already had volumes; agents' editable config is mounted (D19). Also fixed a latent
  `appSpec` aliasing bug that could mutate the catalog's shared volume map. *(One-time caveat: an
  already-running open-webui/comfyui re-inits once when the volume is first attached; fresh
  installs are unaffected.)*
- **Update source — Cloudless validated manifest** (chosen over raw upstream `:latest`): the OS
  fetches a Cloudless-hosted JSON pinning each app to a **tested** image (ideally by digest).
  "Update available" = running digest ≠ manifest digest. Keeps the curation/reliability bet,
  gives controlled rollout and one-click rollback to the prior pinned digest.
- **App-update mechanism (built):** new `engine.ImageDigest` (local repo digest via
  `docker image inspect`) + `engine.RemoteDigest` (registry digest via `docker buildx imagetools
  inspect`, no pull). `GET /api/apps/{id}/update` reports `{installed, updatable, hasUpdate,
  current, latest, checkError, build}` by comparing the two; `POST` applies by reusing
  `runInstall` (pull latest → remove old container → run `app.Spec()` with the **same volumes**),
  so config/data are preserved. Each app's Settings page shows an **updates** section
  (Checking… → "Update available / Update now" / "Up to date / Check again", or "Rebuild" for the
  locally-built agents). For now the catalog's image ref *is* the manifest (validated tags); a
  remote Cloudless manifest can later override the desired digest. **Verified** against the live
  images: open-webui/comfyui report up-to-date (local digest == remote), openclaw reports
  `build:true`. Engines (vLLM/SGLang) update is a later add via the AI page.
- **Manifest consumption (built + live):** new `internal/manifest` package fetches a hosted JSON
  (`CLOUDLESS_MANIFEST_URL`, default `https://samuelcardillo.com/cloudless/cloudless-apps-manifest.json`),
  caches ~10 min, serves stale on error, falls back to catalog tags when unset/offline. When a
  pin exists the daemon pulls/runs **`image@digest`** instead of the moving tag — wired into
  install/update (`runInstall`), the update check (installed digest vs the **pinned** digest;
  surfaces `source/channel/verified/notes`), **provisioning** (boot pulls the validated apps +
  engine image, appends `--revision <sha>` for the pinned default model), and the **infra**
  sidecars (cloudflared/socat). The Settings update section shows "Cloudless validated · <channel>
  — ✓ tested / pinned, not yet hardware-tested". **Verified live** against the hosted file:
  open-webui `source:cloudless, verified:true`, comfyui `verified:false`, both matched to their
  pinned digests. **OpenClaw/Hermes are now registry images too:** their locally-built images were
  pushed to Docker Hub (`samuelcardillo/cloudless-openclaw:v1`, `…-hermes:v1`) and the catalog
  switched from build-on-device → pull-from-registry (embedded Dockerfiles stay as the rebuild
  source). They're pinned in the manifest like every other app, so *everything on the box* —
  apps, agents, infra, model — is a pinned, validated version. Update flow for the agents:
  rebuild from `internal/apps/<name>`, `docker push`, paste the new digest into the manifest.
- **OS updates — same OCI model:** with the immutable/atomic base (bootc / Universal Blue, the
  leaning per D-base), the OS is an OCI image; `bootc upgrade` pulls + stages it for next boot
  with **automatic rollback** and preserves `/var`+`/home` (incl. app volumes). Apps and OS thus
  share one story: versioned OCI images, pulled from Cloudless's registry, validated, atomic,
  data-preserving.

## D31 — Remove Ollama entirely
**Date:** 2026-06-20 · **Status:** Accepted (supersedes the D12 "kept as optional engine")

Ollama was demoted to a non-default optional engine when we moved to vLLM/SGLang (D12), but never
removed. Per the user ("we shouldn't have Ollama"), the catalog entry, glyph and tile styling are
deleted. The only remaining reference is Open WebUI's `ENABLE_OLLAMA_API=False`, which *disables*
Open WebUI's built-in Ollama support so it only talks to the Cloudless engine — kept intentionally.
Catalog is now: vllm, sglang, open-webui, comfyui, ai-toolkit, unsloth, openclaw, hermes.

---

## D32 — Model Manager (curated models + "fits your VRAM")
**Date:** 2026-06-21 · **Status:** Accepted (v1 built)

A model browser on the Settings → Cloudless AI page, inspired by LM Studio (hardware-fit
indicator + one-click use) and vLLM Studio (model lifecycle). Realised against our architecture:
the engine serves **one model at a time**, and vLLM downloads weights from HF on serve, so
"use a model" = restart the engine on it (the existing `POST /api/settings/model` async job,
which streams the download/restart progress).

- **Curated, NON-GATED catalog** (`internal/models`): every entry is vLLM-servable and open on
  HF (no license wall / token needed for auto-download) — deliberately Qwen2.5-heavy (1.5B→72B,
  incl. 4-bit AWQ and Coder variants) plus Phi-3.5-mini. Curation = the reliability bet, same as
  apps. Each has rough `MinVRAMGB` (weights + KV headroom).
- **`GET /api/models`** returns the catalog with a per-model **fit** verdict (`fits` / `tight` /
  `over`, computed vs summed GPU VRAM with an 0.85 comfort factor), the active model, and whether
  the current model is a custom (non-catalog) id.
- **UI:** cards with name, params/quant/context/~VRAM, a colour-coded fit dot (green/amber/red),
  a `code` tag for coding models, the active marker, and click-to-use (confirm for `over` models).
  Engine pills kept; a collapsible **Advanced** holds the custom-HF-id field (the old behaviour).
- **Two sections + hosted "Cloudless highlights" + capability tags (follow-up, built):** the page
  now shows **"Your models"** (what's downloaded — detected by scanning the `cloudless-hf` cache
  volume via a throwaway busybox `ls`, exposed as `engine.Output`) and **"Cloudless highlights"**
  (curated picks you haven't downloaded yet). Highlights come from a **hosted models manifest**
  (`CLOUDLESS_MODELS_URL`, default `…/cloudless-models.json`, cached ~10 min, falls back to the
  built-in `internal/models` list when unreachable) so the company can curate remotely without an
  OS rebuild. Each model carries a **description**, **use-case tags**, and **toolCalling / vision**
  capability badges. `GET /api/models` → `{gpuVRAMGB, current, yours[], highlights[]}` each with
  fit/active/downloaded.
- **Verified:** 31 GB detected; the HF-cache scan put the downloaded default under "Your models"
  and the rest under highlights; vision/tools flags correct (Qwen2.5-VL = vision, Qwen2.5 = tools);
  72B = `over`. Builds/vets/`node --check` clean.
- **Standalone, dashboard-accessible (not in Settings):** the Model Manager is its own large
  full-screen overlay (`#models`, ~`min(1280px,96vw) × 92vh`), opened from the main screen — a
  **◈ button in the menubar** and a **"◈ Model Manager →" link in the Graphics card** (shows the
  active model name). It is **models only** — the inference-engine switch is NOT here (it's an
  advanced, rarely-touched setting). Engine switching lives on a **Settings → "Inference engine"**
  page; `switchEngine`/`trackEngineJob` refresh `#settings`.
- **Image models too (diffusion), same pattern:** the Model Manager has **Language / Image
  tabs**. The Image tab mirrors the LLM one — "Your image models" (files scanned from the ComfyUI
  volume) + "Cloudless highlights" (curated open diffusion models: SD1.5, SDXL, SDXL Turbo, FLUX
  schnell) with base/tags/VRAM-fit. New `internal/diffusion` catalog + `manifest.DiffusionStore`
  (hosted `cloudless-diffusion.json`, fallback to built-in). Mechanics differ from LLMs: diffusion
  models are **files** placed in ComfyUI's `models/checkpoints` (no "serve/restart"), so getting
  one = a **download into the ComfyUI volume**. `GET /api/diffusion`; `POST /api/diffusion/{id}/
  download` runs a job that `curl`s the file in via a `curlimages/curl --user 0` throwaway
  container (root perms + TLS validation; `engine.Output`). **Verified:** endpoint + fit + the
  scan (a file dropped in the volume shows under "Your image models") + the curl-into-volume
  download recipe. **Caveats:** (1) ComfyUI is crash-looping on Blackwell (sm_120) here, so the
  image side can't be used until that's fixed; (2) model URLs/sizes are best-effort — only a tiny
  test file was actually downloaded, not real multi-GB weights; add a sha256 check before trusting.
- **Click → detail → explicit actions (not click-to-act):** clicking a model card no longer
  switches/downloads. It opens an in-overlay **detail view** (reusing the launcher's app-detail
  styling, with a "← All models" back button) showing the full id/file, specs, license,
  description, capability badges, tags, and a VRAM-fit line, plus an explicit **action set**:
  - LLM: **Launch** (set + restart engine, the existing `/api/settings/model` job) and, when not
    on disk, **Download only** — a new **`POST /api/models/download`** that pre-fetches weights
    into the `cloudless-hf` cache via the engine image (`--entrypoint python3 … snapshot_download`)
    so a later launch is instant. Active model shows "✓ Loaded in vLLM right now" + a re-download.
  - Image: **Download** (the existing diffusion job); once present it says "choose it in ComfyUI's
    checkpoint menu" (no launch — ComfyUI picks per-workflow).
- **Launch warning:** before launching, a confirm explains exactly what happens — the engine
  restarts, the currently-loaded model (named) is unloaded, in-progress chats are interrupted
  (~30–60 s), plus extra lines if it isn't downloaded yet (weights download first) or is `over`
  VRAM (may fail to load). The advanced custom-HF-id field launches directly (explicit by nature).
- **Dashboard shows the loaded model:** the Graphics-card link now reads "◈ <model> · loaded →"
  (or "· loading…" while the engine warms up), driven by `refreshActiveModel` (`/api/settings` +
  `/api/engine`), and refreshes after a switch. **Verified:** GET `/api/models` (31 GB, current
  Qwen2.5-1.5B), download endpoint validates (400 on empty/bad id); build/vet/`node --check` clean.
- **Design pass (v2):** the manager was reworked from a settings-page look into a dedicated,
  LM-Studio-style surface. A **toolbar** sits under the tabs: a hardware summary ("Your GPU:
  31 GB · green fits comfortably"), a **search box** (matches name/id/family/params/tags/desc),
  and **capability filter chips** (Fits my GPU · Vision · Tool-calling — image tab shows only
  Fits) that filter the already-fetched lists client-side without a re-fetch, with an empty
  state + "Clear filters". The two tabs share one `paintModelList(c, data, kind)` renderer. The
  **detail view** was rebuilt (no longer borrows the app-launcher layout): an icon-tile hero
  with the name + mono HF id + capability/use tags, a colour-coded **status banner** (blue
  "loaded" / green "ready" / neutral "not downloaded"), the description, a bordered **spec grid**
  (params, quant, context, VRAM, license), the fit line, and the action row. Verified by
  rendering the real CSS in a static harness via headless Edge (light + dark themes) — all
  var-driven, legible in both; `node --check` + `go build` clean.
- **Follow-ups:** multi-backend (llama.cpp/MLX, per vLLM-Studio), per-model advanced config
  (context/quant), pinning user-selected model revisions via the manifest, sha256 on downloads,
  a real progress bar for the pre-download job (snapshot_download output isn't streamed yet).

---

## D33 — Desktop (dashboard) redesign for ergonomics + clarity
**Date:** 2026-06-22 · **Status:** Accepted (built)

The home screen led with an 84px clock (the biggest, highest-contrast, *least* actionable
element — and a duplicate of the menubar clock), while the app launcher (the primary task)
sat at the very bottom under ambient status cards. Reworked the information hierarchy:

- **Compact hero.** The clock shrank 84px → 40px and moved to a quiet top-right anchor with
  the date beneath it (`.hero-time` / `.ht-clock` / `.hero-date`); the greeting moved into the
  subtitle ("Good afternoon. Your private, local-AI workstation."). Reclaims ~150px of prime
  space and stops the eye landing on the time. Seconds dropped from the big clock.
- **Apps promoted.** "Your apps" (the pinned fast-launch tiles, renamed from "Fast launch")
  now sits directly under the hero — the thing you came to do is first. The Graphics + Places
  status cards drop below it. Entrance-animation stagger reordered to match.
- **Better width use.** `main` 1080 → 1140px, hero/section spacing tightened, ask bar widened
  470 → 560px so the primary "ask Cloudless" entry is more prominent.
- **Verified** by rendering the real CSS/markup in a static harness (`scripts/desk-harness.ps1`)
  via headless Edge in both the light (`day`) and dark (`night`) time-of-day themes — legible and
  balanced in both; `node --check` + `go build` clean. (Live-page screenshots weren't possible:
  WSL2 localhost forwarding is off on this box, so the harness renders the embedded file directly.)

---

## D34 — Machine info, user profile, region awareness (France → Mistral)
**Date:** 2026-06-24 · **Status:** Accepted (built)

The OS now knows more about the machine and its user, and tailors itself by region —
all detected **locally**, with **no IP/geo network lookup** (keeps the privacy promise).

- **`internal/locale`** infers timezone (from `$TZ` / `/etc/timezone` / `/etc/localtime`),
  UTC offset, and country — from the **locale** (`fr_FR` → FR) preferring it, else a curated
  **IANA-zone→country** map (France covered incl. overseas territories). No network.
- **`hardware.Sys()`** reports host, distro (`/etc/os-release`), kernel, arch, CPU, cores,
  RAM, uptime (`/proc`). Exposed at **`GET /api/system`**.
- **User profile** (`state.Profile{Name, Region}`) with **`GET/POST /api/profile`**. Region
  is an optional override; "" = auto-detect. `effectiveCountry()` = override ?: detected, and
  is the single source of truth for region features.
- **France → Mistral.** `models.Model` gained `Region` + `Gated`. Two French-built Mistral
  models (`Mistral-7B-Instruct-v0.3`, `Mistral-Nemo-Instruct-2407`, both Apache-2.0) carry
  `Region:"FR"`. `modelsList` flags region-matched models `recommended`, floats them to the
  top, and **merges them in even when the hosted manifest omits them** — so Mistral always
  surfaces on a French machine. They're marked `gated` (their HF repos require accepting terms);
  honest badge + note in the UI. **Caveat:** one-click download of gated repos needs a HF token —
  not yet supported (follow-up: a HF-token field in the profile).
- **UI:** Settings → **Profile** (name, region override, detected timezone/locale/country, a
  "France detected" note); the renamed Settings → **Machine** page adds a **system** block
  (device/OS/kernel/CPU/RAM/uptime/timezone/region); the dashboard hero greets by **name** and
  shows the **timezone · region (🇫🇷)** under the clock; the Model Manager gets a **"Recommended
  in <country>"** section with ★/gated badges. Verified: backend live (TZ/LANG-driven, FR→Mistral
  vs US→none), UI via static harness (`scripts/feat-harness.ps1`); build/vet/`node --check` clean.

---

## D35 — Open WebUI: optional account login + "manage in its admin panel"
**Date:** 2026-06-24 · **Status:** Accepted (built)

Open WebUI ran with `WEBUI_AUTH=False` (no login wall — fine for a single-user
appliance). Added Cloudless settings to **require accounts** and to explain that the
rest of OWUI is managed in OWUI's own admin panel, with default admin credentials.

- **Env-injected config (new `ConfigFile.Env`).** OWUI reads its config from process
  env, not a mounted file — so a config file flagged `Env:true` is parsed and **overlaid
  onto the container env** instead of mounted. Implemented once in `apps.EnvOverrides`
  and applied by **both** `api.appSpec` (settings change → `restartApp`) **and**
  `provision` (boot) — so the choice **survives a reboot** (provision builds from
  `app.Spec()`, which would otherwise ignore it).
- **Settings (generic form):** `requireAuth` → `WEBUI_AUTH`, `allowSignup` →
  `ENABLE_SIGNUP`, written to `webui.env`. Defaults: off / on (matches the prior
  no-login default; sign-ups on so the first admin can be created).
- **Admin info (new `App.Admin`):** an "administration" panel on the app's settings page
  explaining OWUI's Admin Panel (app → top-right → Admin Panel) and showing the **default
  admin credentials** with a "change immediately" warning. Returned by `appSettingsGet` as `admin`.
- **Credentials must match OWUI's real built-in account (bug fixed).** First attempt showed
  invented creds (`admin@cloudless.local` / `cloudless`) — login failed, because OWUI never
  created that account. Per OWUI source, running with `WEBUI_AUTH=False` (our default) makes
  the `signin` path **auto-create and persist a real admin** hardcoded as **`admin@localhost`
  / `admin`** (first user → promoted to admin). So that account already exists in the volume;
  enabling login lets you sign in with it. We therefore surface exactly `admin@localhost` /
  `admin` (with a strong "change it now" note). Seeding a *different* admin via the signup API
  is impossible here — a user already exists, so the first-user-admin rule no longer applies.
- **Share-online safety gate.** Exposing an app publicly via cloudflared while it has no
  login is dangerous (anyone with the link uses your AI + GPU). The app settings page now
  derives a `shareWarn` from the auth fields: if `requireAuth` is off (or it's on but
  `allowSignup` is open), the "share online" block shows an inline ⚠ caution **and**
  enabling the tunnel triggers a blocking confirm. Generic — keyed off the `requireAuth`
  field, so any future app that declares one is covered.
- **Verified:** settings GET returns the toggles + admin block; toggling `requireAuth`
  writes `WEBUI_AUTH=true` to `webui.env` and reads back true; build/vet/`node --check`
  clean; settings page + share warning rendered via `scripts/owui-harness.ps1`.

---

## D36 — Cloudless Proxy: serve your model as an OpenAI API with your own keys
**Date:** 2026-06-26 · **Status:** Accepted (built)

Users wanted to **serve the local model to other people** with API keys they control.
Rather than ship LiteLLM (extra container, Python, DB, and not fully free), we built a
thin **in-daemon gateway** — we have exactly one backend (the active engine, already
OpenAI-compatible behind the `cloudless-ai` alias), so "act like LiteLLM" reduces to
auth + key management in front of one endpoint (~a few hundred lines of stdlib Go).

- **Separate listener.** A second `http.Server` on **`127.0.0.1:8766`** (`CLOUDLESS_GATEWAY_ADDR`),
  distinct from the no-auth dashboard (`:8765`) so it can be exposed independently. `/v1/*`
  is key-checked then **`httputil.ReverseProxy`**'d to the engine (`127.0.0.1:8000`), with
  `FlushInterval=-1` so token streaming (SSE) passes through. It **rewrites the request
  `model`** to the served name (`cloudless`), so any OpenAI client "just works".
- **Keys in `state`.** `APIKey{id,name,prefix,hash,created,lastUsed,requests}` — the full
  secret (`sk-cloudless-…`) is shown **once** at creation; only its **SHA-256 hash** is stored.
  `AddAPIKey`/`DeleteAPIKey`/`ValidateAPIKey`. Per-request usage is counted in memory
  (`RecordUsage`) and flushed lazily (`PersistIfDirty`, 20s ticker + on shutdown) so the
  proxy never hits disk per call.
- **Exposure reuses the app machinery** (per the chosen path): the same host-networked
  **socat** (LAN) and **cloudflared** (public link) sidecars, pointed at the gateway port —
  `gatewayLanName`/`gatewayTunnelName`. The proxy stays key-protected regardless.
- **UI:** Settings → **API access** — base URL + served model + a copy-paste `curl`, key
  list (name / prefix / request count / last used / revoke), one-time key reveal, and the
  LAN + "public link" toggles with a security warning (anyone with a key can use your GPU).
- **Deliberately out of v1** (noted for later): per-key rate limits, budgets, token metering,
  multi-model routing, and a stable bring-your-own-domain tunnel. Easy to layer on the key store.
- **Verified end-to-end:** no key → 401, bad key → 401, valid key → proxied to the engine
  → 200, request count incremented, revoke → 401 immediately; build/vet/`node --check` clean;
  API-access page rendered via `scripts/api-harness.ps1`.

---

## D37 — Inference activity dashboard (live engine metrics + charts)
**Date:** 2026-06-27 · **Status:** Accepted (built)

A real-time view of what the inference engine is doing — prefill/decode throughput,
running vs queued requests, KV-cache use, TTFT/TPOT — with readable numbers and charts.

- **Source = the engine's Prometheus `/metrics`** (host `127.0.0.1:8000/metrics`), scraped
  by `GET /api/engine/metrics` and **normalized** across engines: vLLM exposes `vllm:*` by
  default; **SGLang needs `--enable-metrics`** (added to its catalog command) and exposes
  `sglang:*`. The handler maps both families onto one snapshot (running/waiting, KV cache,
  cumulative prompt/gen tokens, TTFT/TPOT sum+count, SGLang's direct `gen_throughput`).
  Unreachable/!prometheus → `{available:false, hint}`. Parser unit-tested for both engines.
- **No chart dependency.** The dashboard is a full-screen overlay (◈-style, opened from a
  menubar **▥** button + a Graphics-card link) that polls every 1s, derives **rates from
  counter deltas** (tok/s, TTFT/TPOT ms) client-side, keeps a 60-sample rolling buffer, and
  draws area+line charts on a **`<canvas>`** (stdlib-only ethos — no Chart.js). Tiles:
  Decoding / Queued / KV cache / Output tok/s / Prefill tok/s / First-token / Per-token.
- **Activation:** vLLM works immediately. **SGLang** must have its container **recreated** to
  pick up `--enable-metrics` (switch engine, or change model — a daemon restart alone won't
  recreate an already-running engine); until then the dashboard shows a friendly empty state.
- **Verified:** parser tests pass (vLLM + SGLang sample exposition); build/vet/`node --check`
  clean; the real `infChart` canvas code rendered against sample data via `scripts/inf-harness.ps1`.
- **Design pass (v2):** reworked into a clearer hierarchy — a **status line** (engine · model ·
  live), a **hero panel** with the headline output tok/s + a large smooth area chart (output
  only; prompt is bursty and would squash the line, so it's shown as a number), and a
  **metric grid**: active requests (sparkline), a **radial KV-cache gauge**, and TTFT/TPOT with
  plain-English captions ("how fast a reply starts", "speed of each token"). Charts gained
  quadratic smoothing; `/api/engine/metrics` now also returns the served `model` for the header.
- **Beauty pass (v4):** a persistent **status pill** in the overlay header (● engine · model ·
  live, synced across tabs); a **gradient hero** with a **glowing** output line + bold peak/prompt
  figures; **hover-lifting** metric cards; a **gradient + glow radial gauge** for KV cache; and
  **engine comparison cards with icon tiles** (🚀 vLLM / 🧩 SGLang / 🦙 llama.cpp) + a gradient
  active state. The metrics endpoint returns the served `model` so the header reads engine + model.
- **Inference hub (v3):** the overlay became the single home for everything inference — a
  **tabbed** surface: **Activity** (the live metrics), **Engine** (the engine switch, moved out
  of Settings), and **API access** (keys + gateway exposure, moved out of Settings). Reuses
  `renderEnginePage`/`renderApiPage` into the tab panel; metrics polling is gated to the Activity
  tab; the engine-switch re-render now targets `#inf-panel`. Settings keeps only Profile / Machine
  / General. Renamed "Inference activity" → "Inference" (menubar ▥, Graphics-card link, title).

---

## D38 — Third engine (llama.cpp), CPU/RAM on the dashboard, engine comparison
**Date:** 2026-06-27 · **Status:** Accepted (built; llama.cpp recipe unverified)

- **llama.cpp as a third engine.** Added to the catalog (`ghcr.io/ggml-org/llama.cpp:server-cuda`,
  `llama-server` flags: `-hf <gguf>`, `--alias cloudless`, `-ngl 999`, `--jinja`, `--metrics`),
  Prefetch + Service + Engine like SGLang. Its value: **CPU+GPU with RAM offload** (`-ngl`), so it
  can serve models that don't fit in VRAM, using compact **GGUF** files. `parseEngineMetrics` now
  also maps `llamacpp:*` (running/waiting/kv/tokens/throughput). **Caveats (unverified recipe):**
  image tag / GGUF repo+quant / flags are best-effort, to validate on hardware; and it serves its
  bundled GGUF — the Model Manager's model pick (HF safetensors) drives vLLM/SGLang, not llama.cpp
  (GGUF model switching is a follow-up).
- **CPU + RAM on the main screen.** New `hardware.Load()` (live CPU% over a 120ms /proc/stat sample
  + RAM used/total from /proc/meminfo) at **`GET /api/sysload`**, polled every 4s and rendered in the
  dashboard card (renamed **Graphics → Hardware**): GPU, then CPU and memory with the same bars.
- **Engine comparison UI.** The Inference → Engine tab is now **comparison cards** (one per engine):
  a "best for" line, key traits (plain English), a runs-on badge (GPU / CPU + GPU), and the active
  one highlighted; non-active cards carry a "Use this engine" action. Replaces the bare pills.
- **Verified:** `/api/engine` lists all three; `/api/sysload` returns live CPU/RAM (Ryzen 9, 4.5%,
  6.4/15.5 GB); build/vet/`node --check` clean; comparison cards + Hardware card rendered via
  `scripts/engines-harness.ps1`.

---

## Open questions (not yet decided)

- **Open-source CloudlessOS?** Leaning yes (trust/community for a privacy brand, like
  Pop!_OS). License TBD.
- **Orchestrator language:** Go vs Python vs Rust.
- **Web UI framework.**
- **Catalog scope:** how curated vs how extensible (defensibility = curation + reliability).

---

## See also

- [[VISION]] — the vision & strategy these decisions support
- [[ARCHITECTURE]] — the technical design these decisions shape
- [[ROADMAP]] — the phased plan these decisions feed into
- [[STATUS]] — living state, including which decisions are pending input
- [[DEV_ENVIRONMENT]] — hardware & WSL2 runbook (referenced by D1, D4, D8, …)
