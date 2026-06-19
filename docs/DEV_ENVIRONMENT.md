# Development Environment

## Machines

### Primary dev box (this machine) — Phase 0 happens here
- **OS:** Windows 11 Pro
- **GPU:** NVIDIA GeForce RTX 5090, 32 GB VRAM
- **Driver:** 595.79 · **CUDA (driver-reported):** 13.2
- **WSL2:** enabled, v2 default. Distro: **Ubuntu 24.04 installed**, systemd active,
  kernel 6.6 WSL2, Linux user `ledomaine` (sudo requires password). `nvidia-smi` works
  inside WSL (sees the RTX 5090).
- Project repo lives at `D:\Cloudless` (Windows side).

### Secondary box — reserved for later (multi-GPU / native testing)
- **OS:** native Ubuntu
- **GPU:** 3× NVIDIA GPUs
- **Status:** all GPUs currently saturated with other workloads → leave alone for now.
  (GPUs can time-share later via `CUDA_VISIBLE_DEVICES` if VRAM allows.)

## WSL2 + GPU setup steps

Key fact: in WSL2 you do **NOT** install a Linux NVIDIA driver. WSL uses the **Windows**
driver; you only install the CUDA toolkit / container toolkit inside Ubuntu.

1. **Install Ubuntu into WSL2** (interactive — run yourself, sets up Linux user):
   ```
   wsl --install -d Ubuntu-24.04
   ```
2. **Verify GPU passthrough** inside Ubuntu:
   ```
   nvidia-smi          # should show the RTX 5090
   ```
3. **Install Docker + NVIDIA Container Toolkit** — use the repo script (reproducible
   runbook). Run inside the WSL Ubuntu shell; it prompts for your sudo password:
   ```
   bash /mnt/d/Cloudless/scripts/setup-wsl-docker.sh
   ```
   Then apply docker-group membership: run `wsl --shutdown` from Windows and reopen Ubuntu.
   (Decision D1: Docker Engine for Phase 0; Podman remains a later candidate.)
4. **Verify container GPU access:**
   ```
   docker run --rm --gpus all nvidia/cuda:12.4.0-base-ubuntu22.04 nvidia-smi
   ```
5. **Manual ComfyUI baseline** — run ComfyUI in a GPU container by hand. This is the
   thing the orchestrator will automate.

## Setup status (update as you go)

- [x] WSL2 enabled (v2 default) on Windows
- [x] Windows NVIDIA driver present (595.79, RTX 5090 visible via `nvidia-smi`)
- [x] Ubuntu 24.04 installed in WSL2 (systemd active)
- [x] `nvidia-smi` verified inside WSL2 (sees RTX 5090)
- [x] Docker Engine (29.6.0) + NVIDIA Container Toolkit (1.19.1) installed via `scripts/setup-wsl-docker.sh`
- [x] Container GPU access verified (`docker run --gpus all ... nvidia-smi` sees the RTX 5090)
- [ ] ComfyUI runs manually in a GPU container

## Notes / gotchas

- WSL2 networking and systemd differ from bare metal; don't bake kernel/init assumptions
  into the orchestrator (see decision D4).
- Never commit model weights — `.gitignore` excludes common weight formats and `models/`.
