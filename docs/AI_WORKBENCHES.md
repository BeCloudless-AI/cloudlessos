# AI training and data workbenches

CloudlessOS exposes four optional, local workbenches in the App Launcher. They are not
preinstalled: installation is explicit because the upstream CUDA images can be large.

| Workbench | Cloudless runtime | Host support | Purpose |
| --- | --- | --- | --- |
| NeMo RL | NVIDIA NeMo RL 0.7.0 | amd64, arm64 | SFT, DPO, GRPO, PPO, distillation, and agentic RL |
| NeMo Data Designer | `data-designer` 0.8.0 | amd64, arm64 | Synthetic data generation, validation, and scoring |
| MoLT | MoLT 0.1.4 | amd64 only | Experimental agentic SFT and asynchronous RL |
| Axolotl | Axolotl 0.9.2, CUDA 13 / PyTorch 2.12 image | amd64, arm64 | YAML-driven full, LoRA, QLoRA, preference, and multimodal fine-tuning |

Each app opens in CloudlessOS as a JupyterLab workbench. Its HTTP port is bound to loopback
only and cannot be shared to the LAN or public gateway. Jupyter authentication is disabled
inside that loopback boundary so the Cloudless app viewer can open it without a second login.

## Persistent storage

The workbenches use separate persistent notebook volumes and share these system volumes:

- `cloudless-hf` for model and dataset downloads;
- `cloudless-training-data` for datasets shared between workbenches;
- `cloudless-training-output` for checkpoints and exported artifacts.

Bundled source and examples remain part of the image. The writable volume is mounted in a
`cloudless` subdirectory so a new or empty volume cannot hide the bundled project.

Uninstalling a workbench removes its container and image through the normal Cloudless app
lifecycle. Persistent named volumes are retained intentionally so uninstalling or upgrading an
app does not erase notebooks, datasets, model caches, or checkpoints.

## Privacy and compatibility

NeMo Data Designer telemetry is disabled by default with `NEMO_TELEMETRY_ENABLED=false`.
Its notebook can call the active Cloudless model over the private `cloudless` container network.
The other workbenches receive NVIDIA GPU access and use the system's shared local model cache.

MoLT's published image is currently amd64-only and targets supported data-center GPU
architectures, so it is hidden on DGX Spark rather than presenting an installation that cannot
run. NeMo RL and Axolotl use upstream multi-architecture images and are available on DGX Spark.

## Reviewed NVIDIA inference models

NVIDIA Cosmos3-Edge and LocateAnything-3B are curated in Model Manager. Cloudless pins each
reviewed Hugging Face commit and launch contract. Cosmos3 uses NVIDIA's recommended vLLM
reasoner image. LocateAnything's supported upstream path is Transformers custom code, so
Cloudless builds a signed-package adapter on the machine and exposes its worker through the
same private Cloudless engine contract (`cloudless` on port 8000).

Their current reviewed launch contracts are single-Spark only. Model Manager does not offer a
distributed cluster launch for them until a multi-node runtime has been explicitly validated;
this avoids treating an aggregate-memory estimate as proof of runtime compatibility.
