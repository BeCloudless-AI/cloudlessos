# cloudlessd — Cloudless orchestrator (Phase 0)

The local daemon that installs, runs, and manages local-AI apps as GPU containers,
and serves the web UI that drives them. This is the Phase 0 prototype (see
`../docs/ROADMAP.md`).

## Layout

```
cmd/cloudlessd/      entrypoint (HTTP server + graceful shutdown)
internal/engine/     container runtime abstraction + Docker (CLI) implementation
internal/catalog/    curated app recipes (Ollama, Open WebUI, ComfyUI)
internal/api/        local HTTP API + embedded web UI (internal/api/web/)
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
| GET    | /api/gpu                  | nvidia-smi summary                       |
| GET    | /api/catalog              | available app recipes                    |
| GET    | /api/apps                 | orchestrator-managed containers          |
| POST   | /api/apps/{id}/start      | pull image + run container               |
| POST   | /api/apps/{id}/stop       | stop container                           |
| POST   | /api/apps/{id}/remove     | force-remove container                   |

## Known Phase 0 limitations (intentional)

- `start` pulls the image synchronously, so the first launch blocks for minutes.
  Next iteration: async install jobs with streamed progress.
- App state is derived from `docker ps` (no separate persistence yet).
- ComfyUI recipe is a placeholder (`Image: ""`) pending a validated image.
- Containers are managed by name (`cloudless-<id>`); one instance per app.
