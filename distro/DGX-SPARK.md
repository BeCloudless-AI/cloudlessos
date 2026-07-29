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

## Connect two to eight DGX Sparks

CloudlessOS includes a Spark-only guided setup under **Settings > Spark
Cluster**. Add one Spark at a time through the three guided screens:

1. Find or enter the other Spark.
2. Confirm the cable and run the readiness check.
3. Verify the peer SSH fingerprint and create the cluster.

For exactly two Sparks, connect matching rear ConnectX-7 ports with one
supported QSFP112 direct-attach copper cable. For three to eight Sparks, connect
every machine to the same compatible managed RoCE v2 QSFP switch; do not
daisy-chain them. When upgrading a direct pair to three nodes, move both
existing Sparks to that switch before adding the third.

NVIDIA currently documents automated Cluster Assistant validation for up to
four switch-connected nodes. CloudlessOS can configure five to eight nodes on
the same switched fabric, but presents that range as advanced: operators must
qualify their switch, cabling, NCCL behavior, and workload at the intended
scale.

Cloudless configures a coordinator and up to seven workers across the dedicated
`10.100.0.0/24` and `10.100.1.0/24` fabric. It leaves Wi-Fi and normal Ethernet
untouched. Each enrolled Spark receives a unique address on both paths. The
wizard rejects duplicate nodes and stops at eight, refuses to overwrite
unrelated routes, stores no administrator password, and removes its restricted
SSH key and network configuration from every node when the cluster is
disconnected.

The status screen verifies the local Netplan configuration, both ConnectX-7
interfaces, every worker, and both paths to every worker. Distributed vLLM
launches one Ray rank per Spark and sets tensor parallelism to the current node
count. This does not turn aggregate memory into one shared-memory computer;
models must support distributed execution.

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

## Custom vLLM and SGLang builds

DGX Spark developers can compile a trusted upstream revision for GB10 and register the resulting
local Docker image in **Settings -> Engine -> Custom engine builds**. The Cloudless terminal is a
full authenticated host shell; install the optional build dependencies with:

```bash
sudo cloudless-developer-tools
```

New terminal sessions expose the DGX OS CUDA toolkit from `/usr/local/cuda` when installed.
Custom images inherit the managed engine's GPU, cache, selected-model, gateway and readiness
contract, while NVIDIA's managed vLLM image remains selectable for recovery.

Custom images currently run on the local Spark only. Cloudless does not copy an arbitrary local
image to cluster workers, so use the managed distributed vLLM path for multi-Spark inference.
The complete build and rollback runbook is
[`../docs/CUSTOM_ENGINES.md`](../docs/CUSTOM_ENGINES.md).

## Cloudless-native recipes

Model Manager's **Recipes** page manages editable Cloudless recipe profiles and
can import and run native `cloudless.recipe/v1` manifests without a third-party runtime.
Recipes use the same native lifecycle model as the local editor, with pinned
sources, explicit commands, cluster requirements, progress, abort, stop, health,
active-model state, and the stable Cloudless API gateway. No external recipe
runtime is installed or required.
