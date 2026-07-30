# Building CloudlessOS

CloudlessOS is built as an Ubuntu Server 24.04 appliance installer. The standard ISO is
an online installer: it contains Cloudless itself, while GPU drivers, the browser,
container images, and models are obtained during installation or first boot.

## Build host

Use Ubuntu 24.04 with at least 20 GB free. WSL2 can build the packages and standard AMD64 ISO,
but it cannot provide a representative firmware, Plymouth, display-manager, or NVIDIA
installation test.

```bash
sudo apt update
sudo apt install -y curl dpkg-dev imagemagick librsvg2-bin xorriso
bash scripts/install-go.sh
bash distro/scripts/test-packages.sh
bash distro/scripts/build-iso.sh
bash distro/scripts/test-iso.sh
```

If Docker is the only Linux build environment available, validate packages in the
official Go build image:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26-bookworm bash distro/scripts/container-test.sh
```

Replace the final script with `distro/scripts/container-build-iso.sh` to produce the
complete ISO from the same container.

Cloudless Debian packages and the signed update repository support both AMD64 and ARM64.
Run `bash distro/scripts/test-architectures.sh` to cross-build and inspect both package
families. The current generic Ubuntu installer ISO intentionally remains AMD64; DGX Spark
uses a signed, reversible layer over NVIDIA's qualified DGX OS rather than a generic
Ubuntu ISO. See [`DGX-SPARK.md`](./DGX-SPARK.md) for installation, recovery, update,
application-compatibility, and diagnostic instructions.

Platform- and version-locked product features use the shared backend capability
contract described in [`CAPABILITIES.md`](./CAPABILITIES.md). Do not fork the
GUI or rely on browser-only hiding for platform-specific behavior.

To qualify a physical DGX Spark without collecting user data or unique device identifiers,
run `bash distro/scripts/collect-dgx-spark-info.sh`. It creates a single inspectable
`cloudless-dgx-spark-report-*.tar.gz` archive in the current directory.

For an emulated firmware smoke test, install QEMU, OVMF, Socat, and ImageMagick and run
`distro/scripts/test-boot.sh`. It captures BIOS and UEFI framebuffers under
`distro/out/boot-tests/`.

The ISO and SHA-256 file are written to `distro/out/`. Set
`CLOUDLESS_BASE_ISO=/path/to/ubuntu.iso` to use an existing Ubuntu Server image.

## Update a development VM without reinstalling

On the Windows development host, deploy interface and orchestrator changes directly to
the installed VirtualBox VM:

```powershell
.\distro\scripts\deploy-vm.ps1
```

The script builds and validates the Debian packages in a cached Docker builder, starts
the `CloudlessOS` VM if needed, prompts for the installer-created user's password, and
updates the orchestrator. It restarts the kiosk session so Chromium immediately loads
the new interface. The localhost-only SSH forwarding rule is removed when deployment
finishes.

Use the package selector when changing another OS component:

```powershell
.\distro\scripts\deploy-vm.ps1 -Packages shell
.\distro\scripts\deploy-vm.ps1 -Packages orchestrator,shell
.\distro\scripts\deploy-vm.ps1 -Packages all
```

Pass `-VMUser yourname` if the administrator account created by the installer is not
`samcllo`. Use `-SkipBuild` to redeploy already-built packages. Branding and hardware
updates may require a reboot; installer, partitioning, and autoinstall changes still
require rebuilding and reinstalling the ISO.

## Update real installations

Physical CloudlessOS computers use the signed repository at
`https://updates.becloudless.ai/apt`, not the VirtualBox deployment script. The
`cloudless-updater` package checks, downloads, installs, health-checks, and can roll back
Cloudless package generations independently of the web interface. Production signing,
release creation, Cloudflare R2 publication, and one-time bootstrap instructions are in
[`UPDATES.md`](./UPDATES.md).

Production updates for both AMD64 CloudlessOS and ARM64 DGX Spark are released
together with `bash distro/scripts/release.sh VERSION stable`. The command runs
both architecture gates and publishes only a complete, signed, publicly
verified generation.

## Installation behavior

The installer asks for networking, target Grstorage, and administrator identity. It has no
default password and does not silently wipe a disk. Cloudless packages are installed from
the ISO after Ubuntu lays down the base system.

On the installed system:

- `cloudless-hardware.service` detects NVIDIA hardware and configures the Ubuntu driver,
  Docker, and NVIDIA Container Toolkit.
- Settings → Machine checks Ubuntu's signed repositories daily for the driver recommended
  for the detected NVIDIA GPU. Installation is user-confirmed, reports progress, warns
  about Secure Boot, and requires a restart before the new kernel driver becomes active.
- `cloudlessd.service` serves the OS control surface on loopback port 8765 and the authenticated
  inference gateway on user-configurable port 8766 by default.
- `cloudless-terminal.service` serves an authenticated `/bin/login` terminal on loopback port
  7681; `cloudlessd` proxies it at `/terminal/` so its floating, persistent tabs stay inside the
  Cloudless interface.
- LightDM signs into an unprivileged `cloudless` account and launches the browser in kiosk
  mode through Openbox.
- `cloudless-firstboot.service` writes `/var/lib/cloudless/validation-report.txt`.

The first hardware setup requires internet access and may take several minutes. A driver
installation may require an additional reboot before `nvidia-smi` becomes available.

## Developer terminal and custom engines

The installed Terminal is a normal authenticated host login. It is not an anonymous root shell
and does not invent a second Cloudless permission model: users retain their normal Linux groups
and may use `sudo` only when their account is authorized.

Compiler packages remain optional to keep the base image small:

```bash
sudo cloudless-developer-tools
```

When `/usr/local/cuda/bin/nvcc` is present, `/etc/profile.d/cloudless-cuda.sh` exposes the CUDA
SDK in new terminal sessions. A source-built vLLM or SGLang Docker image can then be registered
from Settings -> Engine without replacing the signed managed engine. See
[`../docs/CUSTOM_ENGINES.md`](../docs/CUSTOM_ENGINES.md).

The locked private inference contract and configurable client gateway are documented in
[`../docs/INFERENCE_API.md`](../docs/INFERENCE_API.md).

## Write the ISO to USB

Verify the SHA-256 file, then use Rufus in DD mode, balenaEtcher, GNOME Disks, or a raw
Linux write. Double-check the target before using `dd`:

```bash
sudo dd if=distro/out/cloudlessos-0.1.0-dev-amd64.iso of=/dev/sdX bs=16M status=progress conv=fsync
```

## Release test matrix

Before publishing an image, test:

1. QEMU with UEFI firmware and an empty virtual disk.
2. QEMU with legacy BIOS.
3. Physical NVIDIA hardware with Secure Boot enabled and disabled.
4. A machine without NVIDIA hardware, which must reach the limited-mode UI.
5. Installation with interrupted networking, which must fail clearly.

This development image is not an offline installer or recovery image. It is not ready for
production distribution until the GPU matrix, update path, recovery, and licensing review
are complete.
