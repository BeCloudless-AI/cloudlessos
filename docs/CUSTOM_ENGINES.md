# Building and registering a custom inference engine

CloudlessOS lets advanced users compile their own vLLM or SGLang runtime and select it
from **Settings -> Engine** without replacing the Cloudless-managed engine. This is useful
for testing upstream commits, custom CUDA kernels, experimental patches, or a build tuned
for one GPU architecture such as the DGX Spark GB10 (`SM 12.1`).

This document is the authoritative custom-engine workflow.

## What Cloudless registers

Cloudless registers a **local Docker image**, not a source directory or Python virtual
environment. The image may be built entirely from source, but it must expose the same
OpenAI-compatible behavior as its selected compatibility base:

- **vLLM:** serves the selected model on port `8000` and responds at `/v1/models`.
- **SGLang:** serves the selected model on port `8000` and responds at `/v1/models`.

The registered image inherits the corresponding managed Cloudless launch contract:

- GPU access and IPC settings;
- the current Model Manager selection;
- the shared Hugging Face model cache;
- private `cloudless` container networking and the `cloudless-ai` alias;
- the Cloudless API gateway and Hermes Agent connection;
- readiness, metrics, load, unload and abort behavior.

The private runtime contract is locked to port `8000` and served model name `cloudless`.
Cloudless removes conflicting saved `--port` and `--served-model-name` overrides when it creates
the container. API users do not connect to that private endpoint; they use the authenticated
client identity described in [INFERENCE_API.md](./INFERENCE_API.md).

## Activation and rollback guardrail

Cloudless does not mark a custom image ready merely because Docker started it. Activation waits
for the permanent internal endpoint `http://127.0.0.1:8000/v1/models` and requires all of the
following:

- the endpoint is reachable within the bounded engine startup window;
- it returns HTTP `200`;
- its body is valid OpenAI-compatible models JSON; and
- the models list contains the internal ID `cloudless`.

The same check applies to bundled engines and native recipes, so a custom build cannot bypass it.
On failure Cloudless removes the target container and stable proxy, stops a distributed worker if
one was started, and persists the engine as unloaded. Model weights and the locally built image
remain available for diagnosis or retry. This fail-closed cleanup prevents a bad image, command or
endpoint from surviving as an endless **starting up** state.

Cloudless automatically detects upstream vLLM images whose entrypoint already contains
`vllm serve`, so it does not append that command twice.

Custom engines are deliberately separate from the signed update channel. Cloudless does
not upload, sign, update or delete the custom image or its source checkout. Managed vLLM
remains available as the recovery path.

## Requirements

- A CloudlessOS installation with Docker and a working GPU runtime.
- Enough free storage for the source tree, build layers and final image. A vLLM build can
  consume many gigabytes.
- Internet access to fetch source and build dependencies.
- A trusted source revision. Do not build an unreviewed pull request on a production system.
- For GPU source builds, a CUDA toolkit compatible with the installed driver.

On CloudlessOS, open **Terminal** from the dock or from **Settings -> Engine -> Custom
engine builds**. This is a normal authenticated Linux login, not a restricted shell.
Administrator commands still require the user's password and `sudo` authorization.

Install the optional compiler toolchain once:

```bash
sudo cloudless-developer-tools
```

On a DGX Spark, starting a new terminal session also exposes the CUDA SDK as:

```bash
echo "$CUDA_HOME"       # /usr/local/cuda
nvcc --version
```

If an older session was already open, run `source /etc/profile.d/cloudless-cuda.sh` or
close and reopen Terminal.

## Recommended: build vLLM from source as an image

Use a dedicated workspace and pin the exact upstream revision. A tag is convenient, but a
full commit SHA is the most reproducible choice.

```bash
mkdir -p ~/Cloudless/Workspace/engines
cd ~/Cloudless/Workspace/engines

git clone https://github.com/vllm-project/vllm.git
cd vllm
git checkout <VLLM_TAG_OR_COMMIT>
git submodule update --init --recursive
```

Build the upstream `vllm-openai` target and give it a unique local tag:

```bash
export DOCKER_BUILDKIT=1

docker build . \
  --file docker/Dockerfile \
  --target vllm-openai \
  --platform linux/arm64 \
  --tag cloudless/vllm-sm121:<VLLM_TAG_OR_SHORT_COMMIT> \
  --build-arg torch_cuda_arch_list="" \
  --build-arg max_jobs=8 \
  --build-arg nvcc_threads=2
```

The empty `torch_cuda_arch_list` asks the current vLLM build system to detect the local GPU
architecture instead of compiling every supported target. This is the recommended starting
point for a machine-specific build. Inspect the build output and confirm it targets the GB10
architecture before treating it as optimized. NVIDIA identifies DGX Spark's CUDA CMake
architecture as `121-real`; the notation accepted by vLLM's build argument can change between
upstream revisions, so only force an explicit value after checking the documentation for the
pinned revision.

For AMD64 CloudlessOS, omit `--platform linux/arm64` or replace it with
`--platform linux/amd64`. The same registration workflow applies.

Do not reuse `latest` for experimental builds. A revision-bearing image tag makes rollback,
bug reports and comparisons deterministic.

### Control build pressure

Compilation can consume substantial CPU and unified memory. Reduce the parallelism if the
desktop becomes unresponsive or the build is killed:

```bash
docker build . \
  --file docker/Dockerfile \
  --target vllm-openai \
  --platform linux/arm64 \
  --tag cloudless/vllm-sm121:<REVISION> \
  --build-arg torch_cuda_arch_list="" \
  --build-arg max_jobs=4 \
  --build-arg nvcc_threads=1
```

`ccache` is included by `cloudless-developer-tools` for native rebuilds. Docker layer reuse
also shortens unchanged image rebuilds; do not use `--no-cache` unless diagnosing the build.

## Verify the image before registration

Confirm that Docker can see the tag and that its architecture matches the machine:

```bash
docker image inspect cloudless/vllm-sm121:<REVISION> \
  --format 'id={{.Id}} arch={{.Architecture}} entrypoint={{json .Config.Entrypoint}}'
```

Optional standalone smoke test:

```bash
docker run --rm --gpus all --ipc=host \
  -p 127.0.0.1:18000:8000 \
  -v cloudless-hf:/root/.cache/huggingface \
  cloudless/vllm-sm121:<REVISION> \
  Qwen/Qwen2.5-1.5B-Instruct \
  --served-model-name cloudless \
  --host 0.0.0.0 \
  --port 8000
```

In another terminal:

```bash
curl --fail http://127.0.0.1:18000/v1/models
```

The exact standalone arguments can vary by vLLM revision. This smoke test is optional;
Cloudless performs its own readiness check during activation.

## Register it in CloudlessOS

1. Open **Settings -> Engine**.
2. Find **Custom engine builds** and click **Register local build**.
3. Enter a display name, for example `SM121 optimized vLLM`.
4. Enter the exact local image tag, for example
   `cloudless/vllm-sm121:v0.17.0-g1a2b3c4`.
5. Choose **vLLM** compatibility.
6. Click **Register build**.

Registration first runs a local `docker image inspect`. It fails without changing engine
state if the image is missing. A successfully registered build appears beside the managed
engines with a **Local build** label.

Click **Use this engine** to activate it. Cloudless stops the previous engine, starts the
custom image with the currently selected model, and waits for `GET /v1/models` to succeed.
The regular loading progress, abort and error reporting remain active during this process.

The same action is available over the loopback API:

```bash
curl --fail-with-body \
  -X POST http://127.0.0.1:8765/api/engines/custom \
  -H 'Content-Type: application/json' \
  --data '{
    "name": "SM121 optimized vLLM",
    "image": "cloudless/vllm-sm121:<REVISION>",
    "base": "vllm"
  }'
```

The response contains the generated engine `id`. Activate it with:

```bash
curl --fail-with-body -X POST \
  http://127.0.0.1:8765/api/engine/<CUSTOM_ENGINE_ID>
```

Follow the returned job through the normal jobs API, or watch the progress in Settings.

## Confirm Cloudless is using the custom build

```bash
curl --fail http://127.0.0.1:8765/api/engine
docker ps --filter 'name=cloudless-custom-' \
  --format 'name={{.Names}} image={{.Image}} status={{.Status}}'
curl --fail http://127.0.0.1:<API_PORT>/v1/models \
  -H 'Authorization: Bearer <CLOUDLESS_API_KEY>'
```

The engine response marks the custom entry with `"custom": true`. Cloudless API clients
continue using the port and model alias shown in **Settings -> API access**; applications and
Hermes continue using the stable internal `cloudless-ai` endpoint. They do not need to know the
image tag. Port `8766` and model alias `cloudless` are defaults, not values a client should assume.

## Roll back safely

Open **Settings -> Engine** and click **Use this engine** on managed vLLM. Cloudless stops
the custom container and recreates the managed engine with the selected model.

After switching away, **Remove** deletes only the Cloudless registration and its stopped
container. It intentionally preserves:

- the custom Docker image;
- the Git checkout and modifications;
- model weights in the shared cache;
- build cache and workspace files.

Delete those manually only when they are no longer needed:

```bash
docker image rm cloudless/vllm-sm121:<REVISION>
```

The API equivalent for removing a registration is:

```bash
curl --fail-with-body -X DELETE \
  http://127.0.0.1:8765/api/engines/custom/<CUSTOM_ENGINE_ID>
```

Cloudless refuses to remove the currently selected custom engine. Switch to a managed
engine first.

## Updating a custom build

Treat a new commit as a new immutable image:

```bash
git fetch origin
git checkout <NEW_TAG_OR_COMMIT>
git submodule update --init --recursive

docker build . \
  --file docker/Dockerfile \
  --target vllm-openai \
  --platform linux/arm64 \
  --tag cloudless/vllm-sm121:<NEW_REVISION> \
  --build-arg torch_cuda_arch_list="" \
  --build-arg max_jobs=8 \
  --build-arg nvcc_threads=2
```

Register the new tag as a separate entry. Keep the known-good registration until the new
one passes model loading and a completion request. This gives developers an A/B and rollback
path without mutable tags.

## Troubleshooting

### `Docker cannot find that image locally`

Check the exact repository and tag:

```bash
docker image ls
docker image inspect <IMAGE:TAG>
```

Registration never pulls a custom image from a registry. Pull or build it explicitly.

### `nvcc: command not found`

Open a new Cloudless terminal, or run:

```bash
source /etc/profile.d/cloudless-cuda.sh
command -v nvcc
nvcc --version
```

If `/usr/local/cuda/bin/nvcc` does not exist, install the CUDA toolkit appropriate for the
machine and driver before compiling.

### Build is killed, freezes, or exhausts unified memory

Lower `max_jobs` and `nvcc_threads`, unload the active model in Settings, and stop unrelated
GPU workloads before rebuilding. Build layers and source trees also require free storage.

### Image starts but never becomes ready

Inspect the generated custom container:

```bash
docker ps -a --filter 'name=cloudless-custom-'
docker logs --tail 300 <CONTAINER_NAME>
```

Common causes are:

- a source revision incompatible with the installed CUDA or PyTorch stack;
- an image for the wrong CPU architecture;
- an unsupported model or model argument;
- a server listening on a port other than `8000`;
- no OpenAI-compatible `/v1/models` endpoint;
- an image whose command-line contract differs from the selected vLLM/SGLang base.

Use **Abort** if loading is still active, then select managed vLLM to recover.

Startup is bounded rather than indefinite. Local engines receive a 15-minute launch window;
reviewed runtimes that must build receive up to 45 minutes; distributed launches receive up to
60 minutes for first-time peer image and model preparation. Expiry produces a failed job and the
cleanup described above.

Do not work around an incompatible image by changing the client-facing API port or model alias.
Those values identify the gateway, while the custom image must satisfy the locked private
vLLM/SGLang contract. Cloudless sanitizes conflicting private port and served-name overrides at
launch.

### The build works directly but fails through Cloudless

Compare the effective launch command in Model Manager's advanced engine settings. A custom
build inherits the managed base command so that model selection and Hermes tool calling keep
working. If an upstream fork changes its CLI, it is not compatible with that base contract
until its image restores the expected CLI or Cloudless gains a dedicated compatibility base.

## Current boundaries

- Custom engines are local to one machine. Cloudless does not copy the image to cluster peers.
- A custom engine is never presented as Cloudless-verified or included in signed updates.
- Only the vLLM and SGLang compatibility contracts are currently accepted.
- Registration accepts container images, not arbitrary host executables or virtual environments.
- Exactly one inference engine runs at a time.

These boundaries keep experimentation reversible while protecting the stable Cloudless API
and managed update path.

## Upstream references

- [vLLM GPU installation and source builds](https://docs.vllm.ai/en/latest/getting_started/installation/gpu/)
- [NVIDIA DGX Spark compilation guide](https://docs.nvidia.com/dgx/dgx-spark-porting-guide/porting/compilation.html)
- [NVIDIA DGX Spark vLLM playbook](https://build.nvidia.com/spark/vllm)
