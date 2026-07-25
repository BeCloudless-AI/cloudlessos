# DGX Spark validation — CloudlessOS 0.2.0

Validated on 2026-07-25 against a physical NVIDIA DGX Spark.

## Host baseline

- DGX OS OTA 7.5.0 / Ubuntu 24.04.4
- ARM64, NVIDIA kernel 6.17.0-1026-nvidia
- NVIDIA GB10, compute capability 12.1
- Driver 580.173.02, host CUDA 13.0
- 20 CPU cores and 124,609 MiB reported unified memory
- Secure Boot enabled
- Docker 29.2.1 and NVIDIA Container Toolkit 1.19.1
- `nvidia.com/gpu=all` CDI device present

No unique device identifiers, network addresses, usernames, or credentials are
recorded in this document.

## Results

- Cross-built all six ARM64 Debian packages and confirmed `cloudlessd` is a
  statically linked AArch64 ELF.
- Ran that exact ARM64 daemon from an extracted package with provisioning
  disabled. `/api/system` detected `dgx-spark`, `/api/gpu` reported the GB10 and
  124,609 MiB as `unified`, and `/api/catalog` selected only ARM64-compatible
  application recipes.
- Confirmed the managed vLLM launch preview uses
  `--device nvidia.com/gpu=all`, NVIDIA's ARM64 vLLM image, `--ipc host`, and
  the recommended memlock/stack limits.
- Ran an NVIDIA CUDA 13.0.1 base container through the same CDI device; it
  reported GB10, driver 580.173.02, and compute capability 12.1.
- Pulled and ran `nvcr.io/nvidia/vllm:26.05.post1-py3`. Its PyTorch runtime
  reported CUDA 13.2 available, NVIDIA GB10, capability `(12, 1)`, and the
  unified-memory capacity.
- Served `Qwen/Qwen2.5-1.5B-Instruct` using the exact Cloudless launch command.
  The health endpoint returned HTTP 200, an OpenAI-compatible chat request
  returned `SPARK READY`, and a required function-call request returned a
  correctly structured `get_system_status` tool call.
- Pulled the official ARM64 Hermes image, confirmed `gateway run` is available,
  and reached its loopback dashboard with HTTP 200. Cloudless's generated
  `API_SERVER_KEY` path is covered separately by orchestrator tests.
- Removed every temporary container, volume, image, binary, package, and log
  used for validation. No Cloudless package was installed and no persistent
  host configuration was changed during qualification.

## Automated release gates

- `go test ./...`
- inline browser JavaScript syntax validation
- AMD64 and ARM64 package build/content validation
- signed release dry run (12 changed package/architecture pairs)
- atomic APT/R2 publication test, including public manifest verification
- shell syntax checks and `git diff --check`
