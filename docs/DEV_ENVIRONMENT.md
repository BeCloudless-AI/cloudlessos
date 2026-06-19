# Development Environment

## Machines

### Primary dev box (this machine) — Phase 0 happens here
- **OS:** Windows 11 Pro
- **GPU:** NVIDIA GeForce RTX 5090, 32 GB VRAM
- **Driver:** 595.79 · **CUDA (driver-reported):** 13.2
- **WSL2:** enabled, version 2 is default. Distro: **installing Ubuntu 24.04** (see status).
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
3. **Install container engine + NVIDIA Container Toolkit** (Docker shown; Podman is an
   alternative under consideration):
   ```
   # Docker Engine (or Docker Desktop with WSL integration)
   # then the NVIDIA Container Toolkit, then:
   sudo nvidia-ctk runtime configure --runtime=docker
   sudo systemctl restart docker   # note: WSL2 systemd may need enabling
   ```
4. **Verify container GPU access:**
   ```
   docker run --rm --gpus all nvidia/cuda:12.4.0-base-ubuntu22.04 nvidia-smi
   ```
5. **Manual ComfyUI baseline** — run ComfyUI in a GPU container by hand. This is the
   thing the orchestrator will automate.

> Exact toolkit install commands depend on current NVIDIA docs — fill in verified
> commands here once run, so this becomes a reproducible runbook.

## Setup status (update as you go)

- [x] WSL2 enabled (v2 default) on Windows
- [x] Windows NVIDIA driver present (595.79, RTX 5090 visible via `nvidia-smi`)
- [ ] Ubuntu 24.04 installed in WSL2
- [ ] `nvidia-smi` verified inside WSL2
- [ ] Container engine + NVIDIA Container Toolkit installed
- [ ] Container GPU access verified
- [ ] ComfyUI runs manually in a GPU container

## Notes / gotchas

- WSL2 networking and systemd differ from bare metal; don't bake kernel/init assumptions
  into the orchestrator (see decision D4).
- Never commit model weights — `.gitignore` excludes common weight formats and `models/`.
