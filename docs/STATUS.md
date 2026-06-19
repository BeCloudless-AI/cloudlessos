# Live Status

> The cold-start anchor. If resuming work, read this first (after `CLAUDE.md`).
> Keep it current — update the date and sections whenever state changes.

**Last updated:** 2026-06-19

## Where we are

In **Phase 0 (orchestrator prototype)**, dev-environment setup step. Ubuntu 24.04 is
installed in WSL2 with working GPU passthrough. Next is installing Docker + the NVIDIA
Container Toolkit. No application code exists yet.

## Done

- Defined vision, architecture (3 layers + thin kiosk shell), roadmap, and decisions.
- Initialized git repo at `D:\Cloudless` with documentation structure.
- Confirmed primary dev box: Windows 11 + RTX 5090 (32 GB), driver 595.79, CUDA 13.2.
- Installed Ubuntu 24.04 in WSL2 (systemd active, user `ledomaine`); `nvidia-smi` works
  inside WSL and sees the RTX 5090.
- Decided Docker Engine for Phase 0 (D1); wrote `scripts/setup-wsl-docker.sh`.

## In progress

- User to run `scripts/setup-wsl-docker.sh` in WSL (installs Docker + NVIDIA Container
  Toolkit), then `wsl --shutdown` to apply docker-group membership.

## Next steps (in order)

1. Run `scripts/setup-wsl-docker.sh`; then `wsl --shutdown` and reopen Ubuntu.
2. Verify container GPU access: `docker run --rm --gpus all nvidia/cuda:...-base nvidia-smi`.
3. Run ComfyUI manually in a GPU container (the manual baseline).
4. Scaffold the orchestrator daemon + minimal web UI in `D:\Cloudless`.

See `DEV_ENVIRONMENT.md` for the setup checklist and commands.

## Decisions pending input

- Orchestrator language (Go / Python / Rust), web UI framework, Docker vs Podman.
- Final shipped-distro base (immutable Fedora-family vs Ubuntu).
- Open-source CloudlessOS? (leaning yes) + license.

## Blockers

- None. Waiting on the WSL2 Ubuntu install to proceed to GPU verification.
