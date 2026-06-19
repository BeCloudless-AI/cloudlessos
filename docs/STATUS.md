# Live Status

> The cold-start anchor. If resuming work, read this first (after `CLAUDE.md`).
> Keep it current — update the date and sections whenever state changes.

**Last updated:** 2026-06-19

## Where we are

In **Phase 0 (orchestrator prototype)**. The dev environment is fully set up and the
core capability is verified: a Docker container can see the GPU (WSL2 → Docker →
NVIDIA Container Toolkit → RTX 5090). No application code exists yet — next is choosing
the orchestrator stack and scaffolding it.

## Done

- Defined vision, architecture (3 layers + thin kiosk shell), roadmap, and decisions.
- Initialized git repo at `D:\Cloudless` with documentation structure.
- Confirmed primary dev box: Windows 11 + RTX 5090 (32 GB), driver 595.79, CUDA 13.2.
- Installed Ubuntu 24.04 in WSL2 (systemd active, user `ledomaine`); `nvidia-smi` works
  inside WSL and sees the RTX 5090.
- Decided Docker Engine for Phase 0 (D1); wrote `scripts/setup-wsl-docker.sh`.
- Installed Docker 29.6.0 + NVIDIA Container Toolkit 1.19.1 in WSL Ubuntu.
- **Verified container GPU access** — `docker run --gpus all` sees the RTX 5090.

## In progress

- Choosing the orchestrator stack (language for the daemon + web UI approach), then
  scaffolding it. (Open decision in `DECISIONS.md`.)

## Next steps (in order)

1. Decide orchestrator language/stack.
2. Scaffold the orchestrator daemon + minimal web UI in `D:\Cloudless`.
3. Run ComfyUI manually in a GPU container (manual baseline) and turn it into the first
   catalog recipe the daemon can install/launch/stop/uninstall.

Note: docker-group membership for `ledomaine` is set but needs a `wsl --shutdown` +
reopen to take effect; until then run docker with sudo.

See `DEV_ENVIRONMENT.md` for the setup checklist and commands.

## Decisions pending input

- Orchestrator language (Go / Python / Rust), web UI framework, Docker vs Podman.
- Final shipped-distro base (immutable Fedora-family vs Ubuntu).
- Open-source CloudlessOS? (leaning yes) + license.

## Blockers

- None. Waiting on the WSL2 Ubuntu install to proceed to GPU verification.
