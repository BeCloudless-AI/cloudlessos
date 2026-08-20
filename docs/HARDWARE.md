# Hardware support and qualification

CloudlessOS targets NVIDIA-capable AMD64 computers and NVIDIA DGX Spark ARM64 systems. This
document describes the support policy; it is not a fixed bill of materials or a promise that every
CUDA-capable device has been qualified.

## Generic NVIDIA systems

The appliance installer targets Ubuntu 24.04 on `amd64`. A supported machine needs:

- an NVIDIA GPU and driver combination supported by the selected engine image;
- enough GPU memory for the model, KV cache and runtime overhead;
- Docker plus NVIDIA Container Toolkit;
- sufficient local storage for model weights, container layers and rollback packages; and
- network access for initial package, image and model downloads.

Cloudless detects NVIDIA accelerators with NVML/`nvidia-smi`, reports per-device memory and load,
and uses the detected capacity for Model Manager fit guidance. Model Manager separately ranks the
admitted catalog entries and marks one balanced **Best for this machine** choice. That ranking uses
only a matching reviewed runtime envelope: memory headroom and the context covered by that exact
profile are always
available, while quality and throughput affect the result only when the signed catalog supplies
their provenance. Models with unknown compatibility or an over-capacity verdict cannot win.
Memory estimates are advisory: the engine, quantization, context length, concurrency and kernel
choice can change actual consumption.

Multi-GPU generic computers remain a qualification target. Cloudless preserves the one-active-
engine invariant and passes the selected topology to engines that support tensor parallelism; it
does not imply that memory from unrelated GPUs behaves like one physically unified pool.

## DGX Spark

DGX Spark uses NVIDIA's qualified DGX OS rather than the generic Cloudless ISO. Cloudless installs
as a reversible signed ARM64 package layer and leaves kernel, firmware, CUDA, driver and container
toolkit ownership with NVIDIA.

On Spark, Cloudless treats GB10 unified memory as the usable accelerator budget, exposes storage
instead of a redundant CPU summary, links to DGX Dashboard, and can guide connection of two to
eight Sparks. One- and two-Spark configurations are the current supported qualification targets.
Three-to-eight-Spark operation is preview until each topology can be exercised on physical hardware;
automated simulations are retained but are not physical evidence. A connected cluster has aggregate
capacity, but every model still needs an engine and recipe that explicitly support the topology.
Replicated weights, KV caches and runtime buffers mean reported memory use is not expected to equal
model size divided by node count.

See [the DGX Spark guide](../distro/DGX-SPARK.md) for installation and cluster operation.

## Custom builds and new GPU architectures

New architectures can require a vLLM or SGLang build newer than the managed Cloudless image. The
supported expert path is to compile a container image on the target machine, validate it, and
register it as a custom engine. This keeps the managed engine available for recovery and keeps local
experimental binaries out of signed OS updates.

Follow [Custom inference engines](./CUSTOM_ENGINES.md). A successful compilation is not by itself a
qualification result: load a representative model and verify readiness, inference correctness,
memory use, restart, unload and rollback.

## Qualification matrix

Before Cloudless advertises a hardware/engine combination as supported, record at least:

| Area | Required evidence |
|---|---|
| Boot/install | clean install or DGX layer install, reboot and kiosk recovery |
| GPU runtime | `nvidia-smi`, container GPU access and engine readiness |
| Models | representative small and near-capacity model load/inference |
| Lifecycle | abort, unload, switch engine and restart recovery |
| Updates | signed update, failed-update rollback and retained local data |
| Thermals | sustained workload without throttling or unsafe temperatures |
| Cluster | discovery, reconnect, peer loss and distributed-engine failure handling |

The live validation state and remaining release gates are tracked in
[Status](./STATUS.md), not in speculative hardware price sheets.
