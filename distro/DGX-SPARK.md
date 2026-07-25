# CloudlessOS for NVIDIA DGX Spark

CloudlessOS is installed on a DGX Spark as a signed, reversible appliance layer
over NVIDIA DGX OS. It does **not** replace the NVIDIA kernel, GPU driver, CUDA,
firmware, Docker, CDI configuration, recovery image, or Secure Boot chain.

This is intentionally different from the generic AMD64 CloudlessOS installer
ISO. DGX Spark is an ARM64 Grace Blackwell appliance whose OS components are
released and qualified together by NVIDIA.

## Install

Start from an updated, working DGX OS installation. Verify that `nvidia-smi`,
`docker info`, and `nvidia-ctk cdi list` work. Download the installer, its
detached signature, and the Cloudless public archive key:

```bash
curl -fsSLO https://updates.becloudless.ai/install-dgx-spark.sh
curl -fsSLO https://updates.becloudless.ai/install-dgx-spark.sh.asc
curl -fsSLO https://updates.becloudless.ai/apt/cloudless-archive-keyring.pgp
test "$(gpg --batch --show-keys --with-colons cloudless-archive-keyring.pgp |
  awk -F: '$1 == "fpr" {print $10; exit}')" = \
  "745BF7A97F64EB716DAF7677974145C2D867C99E"
gpgv --keyring ./cloudless-archive-keyring.pgp \
  install-dgx-spark.sh.asc install-dgx-spark.sh
sudo bash install-dgx-spark.sh
```

Do not execute the installer unless both the pinned fingerprint check and
`gpgv` succeed. This verifies the file before any of its code runs.

The default appliance mode selects the Cloudless kiosk at boot. To keep the
normal NVIDIA GNOME login and use Cloudless in a browser instead:

```bash
sudo bash install-dgx-spark.sh --side-by-side
```

The installer refuses to run on a non-ARM64 or non-Spark machine. Before making
changes it verifies the signed Cloudless APT metadata, checks the GPU, Docker and
the `nvidia.com/gpu=all` CDI device, then records recovery metadata under
`/var/lib/cloudless/dgx-backup/`.

Run a completely read-only production readiness check with:

```bash
sudo bash install-dgx-spark.sh --check
```

The stable repository must contain ARM64 packages before using the production
installer. The production release command tests, signs, publishes, and publicly
verifies AMD64, ARM64, this installer, and the application manifest together.

## Switch desktops or recover

Switch back to NVIDIA's desktop without removing Cloudless:

```bash
sudo cloudless-dgx-desktop-mode dgx
sudo reboot
```

Return to the Cloudless appliance:

```bash
sudo cloudless-dgx-desktop-mode cloudless
sudo reboot
```

Both paths keep `graphical.target`; the command changes only the active display
manager and the explicit DGX appliance marker.

## Updates

Cloudless packages update from `https://updates.becloudless.ai/apt`. NVIDIA
drivers, CUDA, firmware and the kernel remain managed by DGX OS. The Cloudless
driver updater reports that ownership instead of trying to install Ubuntu's
generic NVIDIA package.

The Cloudless branding package preserves the DGX OS release identity required
by NVIDIA's OTA tooling. It may apply Cloudless Plymouth/GRUB artwork, but it
does not divert `/usr/lib/os-release` on a Spark.

## Diagnostics

Generate a shareable text report:

```bash
cloudless-diagnostics
```

It includes platform, GPU/CDI, containers, Cloudless service logs and local API
health. It deliberately excludes application configuration and credentials.
First-boot validation is written to
`/var/lib/cloudless/validation-report.txt`.

## Application behavior

- vLLM uses NVIDIA's ARM64 Grace Blackwell container.
- SGLang uses its CUDA 13.0 ARM64 image.
- ComfyUI uses its Ubuntu 24 / CUDA 13.2 DGX image.
- Open WebUI, Hermes, llama.cpp, Cloudflared and socat use multi-architecture
  images.
- Apps without a validated ARM64 recipe are not shown on the Spark. They are
  never presented as installable and then allowed to fail.

The UI reports GB10 memory as unified system/accelerator memory rather than
pretending it is dedicated VRAM. Model fit estimates use that same capacity.
