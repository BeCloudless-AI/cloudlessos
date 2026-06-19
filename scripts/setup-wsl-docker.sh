#!/usr/bin/env bash
#
# Cloudless dev setup: Docker Engine + NVIDIA Container Toolkit on WSL2 Ubuntu.
#
# Run from inside your WSL Ubuntu shell (it will prompt for your sudo password):
#     bash /mnt/d/Cloudless/scripts/setup-wsl-docker.sh
#
# After it finishes, run `wsl --shutdown` from Windows (PowerShell) and reopen
# Ubuntu so your new `docker` group membership takes effect, then verify with:
#     docker run --rm --gpus all nvidia/cuda:12.4.0-base-ubuntu22.04 nvidia-smi
#
# NOTE: In WSL2 you do NOT install a Linux NVIDIA driver — WSL uses the Windows
# driver. We only install Docker + the NVIDIA Container Toolkit here.

set -euo pipefail

echo "==> Installing Docker Engine prerequisites"
sudo apt-get update
sudo apt-get install -y ca-certificates curl gnupg

echo "==> Adding Docker's official APT repository"
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "${VERSION_CODENAME}") stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

echo "==> Installing Docker Engine"
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io \
  docker-buildx-plugin docker-compose-plugin

echo "==> Adding NVIDIA Container Toolkit APT repository"
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
  | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
  | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
  | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list > /dev/null

echo "==> Installing NVIDIA Container Toolkit"
sudo apt-get update
sudo apt-get install -y nvidia-container-toolkit

echo "==> Configuring Docker to use the NVIDIA runtime"
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker

echo "==> Enabling Docker on boot and adding $USER to the docker group"
sudo systemctl enable docker
sudo usermod -aG docker "$USER"

echo
echo "==> Done."
echo "    Next: run 'wsl --shutdown' from Windows, reopen Ubuntu, then verify:"
echo "      docker run --rm --gpus all nvidia/cuda:12.4.0-base-ubuntu22.04 nvidia-smi"
