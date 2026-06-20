# cloudlessd — Cloudless orchestrator (Phase 0)

The local daemon that installs, runs, and manages local-AI apps as GPU containers,
and serves the web UI that drives them. This is the Phase 0 prototype (see
`../docs/ROADMAP.md`).

## Layout

```
cmd/cloudlessd/      entrypoint (HTTP server + graceful shutdown)
internal/engine/     container runtime abstraction + Docker (CLI) implementation
internal/catalog/    curated app recipes (Ollama, Open WebUI, ComfyUI)
internal/jobs/       async install jobs + progress fan-out (SSE)
internal/hardware/   host GPU stats via nvidia-smi
internal/places/     well-known folders + open-in-file-manager
internal/provision/  pre-install bundled apps on startup (shared network + default model)
internal/apps/       embedded Dockerfiles for locally-built apps (OpenClaw, Hermes)
internal/state/      per-user persisted state (first-run/onboarding)
internal/api/        local HTTP API + embedded web UI (internal/api/web/)
                     web/vendor/ holds offline-vendored Three.js + Red Hat Mono
```

Module path `github.com/cloudless/orchestrator` is a placeholder until we pick the
real repo home. Stdlib-only — no external dependencies, builds offline.

## Run (in WSL Ubuntu)

```
cd /mnt/d/Cloudless/orchestrator
go run ./cmd/cloudlessd
# then open http://localhost:8765
```

Requires Docker + the NVIDIA Container Toolkit (see `../scripts/setup-wsl-docker.sh`).
The daemon binds `127.0.0.1:8765` by default (override with `CLOUDLESS_ADDR`).

## API

| Method | Path                      | Purpose                                  |
|--------|---------------------------|------------------------------------------|
| GET    | /api/health               | daemon + docker reachability             |
| GET    | /api/gpu                  | `{available, gpus[]}` (per-GPU stats)    |
| GET    | /api/catalog              | available app recipes                    |
| GET    | /api/apps                 | orchestrator-managed containers          |
| POST   | /api/apps/{id}/start      | start async install job; returns `jobId` |
| POST   | /api/apps/{id}/stop       | stop container                           |
| POST   | /api/apps/{id}/remove     | force-remove container                   |
| GET    | /api/jobs/{id}            | install job state snapshot               |
| GET    | /api/jobs/{id}/events     | install job progress (Server-Sent Events)|
| GET    | /api/onboarding           | first-run state (`completed`,`firstLaunch`)|
| POST   | /api/onboarding/complete  | mark first-run onboarding done           |
| GET    | /api/folders              | well-known folders (Models/Outputs/…)    |
| POST   | /api/folders/{id}/open    | create if needed + open in file manager  |
| GET    | /api/engine               | active inference engine + readiness      |
| POST   | /api/engine/{id}          | switch engine (vllm/sglang); async job   |
| GET    | /api/settings             | current model + default                  |
| POST   | /api/settings/model       | set model + restart engine; async job    |
| POST   | /api/apps/{id}/reset      | remove + (rebuild) + reinstall; async    |
| POST   | /api/apps/{id}/uninstall  | remove container + image                 |
| GET    | /api/apps/{id}/config     | editable config files + content          |
| POST   | /api/apps/{id}/config     | write config + restart app; async        |
| POST   | /api/apps/{id}/config/reset | restore default config + restart       |
| POST   | /api/onboarding/reset     | replay the welcome tour                  |

First-run state is owned by the daemon (`internal/state`), persisted to a per-user JSON
file (`CLOUDLESS_STATE_DIR` → `$XDG_STATE_HOME/cloudless` → `~/.local/state/cloudless`).
"First launch" = no prior state file at startup. See docs/DECISIONS.md D8.

Install is asynchronous: `start` returns a `jobId` immediately and the daemon pulls +
runs in the background, streaming progress (per-layer pull counts, then start/running) to
`/api/jobs/{id}/events`. The web UI consumes this via `EventSource`.

## Pre-installed apps (D11)

On startup the daemon provisions the bundled apps onto a shared `cloudless` docker network:

- **vLLM** (`cloudless-vllm`) — the default inference engine (D12), OpenAI-compatible on
  `:8000`, serving the model named `cloudless`. Default `Qwen/Qwen2.5-1.5B-Instruct`;
  override with `CLOUDLESS_DEFAULT_MODEL` (any Hugging Face id). Validated on Blackwell.
- **Open WebUI** (`cloudless-open-webui`) — branded "Cloudless AI", no login wall, wired to
  vLLM via the OpenAI API (`http://cloudless-vllm:8000/v1`). The hero "Chat with your
  Cloudless AI" button opens it.
- **ComfyUI** (`cloudless-comfyui`) — image pinned but not yet validated on Blackwell.

- **SGLang** — alternative engine, **pre-fetched** (image pulled, ~41.6 GB) but not run by
  default; switch to it instead of vLLM. (`Prefetch` apps are pulled, not started.)

**Engine switching (D15):** all clients use one stable endpoint `http://cloudless-ai:8000/v1`.
The active engine owns the `cloudless-ai` alias on fixed port 8000 and serves model id
`cloudless`, so switching engines is transparent to Open WebUI and the agents. Switch via
`POST /api/engine/{vllm|sglang}` or the engine pills in the UI; the choice persists and
exactly one engine runs at a time.

Ollama is kept as an optional, non-default engine (not auto-provisioned). **OpenClaw** and
**Hermes** are AI agents with no upstream image, so they're **built locally** from embedded
Dockerfiles (`internal/apps/`) on first install and pre-wired to Cloudless AI (D14). Reset
everything with `../scripts/reset-apps.sh`.

## Known Phase 0 limitations (intentional)

- Pull progress is layer-level (N/total layers), not byte-level percentages — docker's
  non-TTY output reports discrete per-layer status, not continuous bytes.
- Job + app state is in-memory / derived from `docker ps`; no persistence across restarts.
- ComfyUI recipe is a placeholder (`Image: ""`) pending a validated image.
- Containers are managed by name (`cloudless-<id>`); one instance per app.
