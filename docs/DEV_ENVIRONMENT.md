# Development environment

This runbook covers orchestrator and interface development. Distribution builds, releases and
physical installation use the additional gates in [`../distro/README.md`](../distro/README.md).

## Supported development setup

- Ubuntu 24.04 native or under WSL2.
- Docker Engine with NVIDIA Container Toolkit when testing GPU workloads.
- Go 1.26 or newer.
- Node.js for embedded-interface JavaScript validation.
- Git and at least 20 GB of free storage; model and engine images require substantially more.

The primary repository checkout may live on the Windows filesystem under WSL. Disable Go VCS
stamping there if Git reports mismatched ownership:

```bash
export GOFLAGS=-buildvcs=false
```

## WSL2 GPU setup

WSL uses the Windows NVIDIA driver. Do not install a second Linux kernel driver inside WSL.

```bash
# Windows, first setup only
wsl --install -d Ubuntu-24.04

# Ubuntu/WSL
nvidia-smi
bash scripts/setup-wsl-docker.sh
bash scripts/install-go.sh
```

Restart WSL after Docker group membership changes:

```powershell
wsl --shutdown
```

Then verify container GPU access:

```bash
docker run --rm --gpus all nvidia/cuda:13.0.0-base-ubuntu24.04 nvidia-smi
```

## Run Cloudless locally

```bash
cd orchestrator
go run ./cmd/cloudlessd
```

Open `http://127.0.0.1:8765`.

For interface work that must not pull or start the managed application set:

```bash
cd orchestrator
CLOUDLESS_NO_PROVISION=1 go run ./cmd/cloudlessd
```

The development state location follows `CLOUDLESS_STATE_DIR`, then
`$XDG_STATE_HOME/cloudless`, then `~/.local/state/cloudless`. Production packages explicitly
use `/var/lib/cloudless`.

## Validate orchestrator and interface changes

```bash
cd orchestrator
go test ./...
go vet ./...
go build ./...
cd ..
node distro/scripts/test-web-js.js
```

The repository helper performs the build and vet steps with the WSL-specific environment:

```bash
bash scripts/build-orchestrator.sh
```

Changes to embedded interface assets, catalog contracts or application Dockerfiles require a
new daemon build. Restart the running daemon after rebuilding; an old process continues serving
its embedded copy.

## Develop on the physical DGX Spark

DGX Spark is ARM64 and retains NVIDIA's qualified driver, CUDA and DGX OS components. Do not
replace those with the generic Ubuntu hardware installer.

Use the signed DGX installer for persistent systems. For a short development iteration, cross-build
the daemon and deploy it without publishing packages:

```bash
cd orchestrator
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/cloudlessd-arm64 ./cmd/cloudlessd
```

After copying it to the Spark, stop `cloudlessd`, install it at
`/usr/lib/cloudless/cloudlessd`, restart the service, verify `/api/health`, and refresh the kiosk.
This direct deployment is temporary: the next signed package update replaces the binary with the
published version.

Use [`../distro/DGX-SPARK.md`](../distro/DGX-SPARK.md) for supported installation, diagnostics,
desktop switching and cluster operations.

## Compile custom inference engines

Installed CloudlessOS systems expose an authenticated full host terminal. The optional build
toolchain is installed with:

```bash
sudo cloudless-developer-tools
```

On CUDA systems, new terminal sessions receive `CUDA_HOME=/usr/local/cuda` and add `nvcc` to
`PATH` when that toolkit exists.

Custom engine builds must be tagged as local Docker images before Cloudless can register them.
See [`CUSTOM_ENGINES.md`](./CUSTOM_ENGINES.md) for the complete source-build, registration,
readiness, rollback and troubleshooting workflow.

## Common gotchas

- Never commit model weights, Hugging Face caches, custom engine images or build output.
- A driver-reported CUDA version is not proof that the CUDA compiler is installed; verify
  `nvcc --version` before a native source build.
- WSL shell quoting is fragile across PowerShell, `wsl.exe` and Bash. Put complex operations in
  repository scripts rather than nested one-line commands.
- Refresh the Linux session after adding a user to the Docker group.
- Custom engine images are trusted local code with GPU and model-cache access.
- Reusing a mutable `latest` tag makes custom-engine rollback and bug reproduction ambiguous.
- Generic AMD64 and DGX Spark ARM64 features share one source tree; use backend capabilities and
  architecture-specific contracts rather than branching the frontend.

## Related documentation

- [Project README](../README.md)
- [Architecture](./ARCHITECTURE.md)
- [Custom engines](./CUSTOM_ENGINES.md)
- [DGX Spark](../distro/DGX-SPARK.md)
- [Distribution builds](../distro/README.md)
- [Live status](./STATUS.md)
