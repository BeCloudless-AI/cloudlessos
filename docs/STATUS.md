# Live Status

> The cold-start anchor. If resuming work, read this first (after `CLAUDE.md`).
> Keep it current — update the date and sections whenever state changes.

**Last updated:** 2026-06-19

## Where we are

Project just bootstrapped. Repo and documentation created. We are at the very start of
**Phase 0 (orchestrator prototype)** — specifically the dev-environment setup step. No
application code exists yet.

## Done

- Defined vision, architecture (3 layers + thin kiosk shell), roadmap, and decisions.
- Initialized git repo at `D:\Cloudless` with documentation structure.
- Confirmed primary dev box: Windows 11 + RTX 5090 (32 GB), driver 595.79, CUDA 13.2.
- Confirmed WSL2 is enabled (v2 default) but no distro installed yet.

## In progress

- Installing **Ubuntu 24.04 into WSL2** (`wsl --install -d Ubuntu-24.04`) — user runs this
  interactively.

## Next steps (in order)

1. Finish Ubuntu 24.04 install in WSL2.
2. Verify `nvidia-smi` works **inside** WSL2.
3. Install Docker (or Podman) + NVIDIA Container Toolkit; verify container GPU access.
4. Run ComfyUI manually in a GPU container (the manual baseline).
5. Scaffold the orchestrator daemon + minimal web UI in `D:\Cloudless`.

See `DEV_ENVIRONMENT.md` for the setup checklist and commands.

## Decisions pending input

- Orchestrator language (Go / Python / Rust), web UI framework, Docker vs Podman.
- Final shipped-distro base (immutable Fedora-family vs Ubuntu).
- Open-source CloudlessOS? (leaning yes) + license.

## Blockers

- None. Waiting on the WSL2 Ubuntu install to proceed to GPU verification.
