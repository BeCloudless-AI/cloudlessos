# syntax=docker/dockerfile:1.7
FROM ghcr.io/anemll/dspark-vllm-gx10@sha256:a83948492cf13df455170fb42885f5ef4db54fefe0feff0f841ecbff464ac9d8

ARG MIALABS_REVISION=3c9576c52ab71d89e22fe4621e0d32300a59039a

# The upstream image predates Ubuntu's fixes for five actionable CRITICAL
# linux-libc-dev findings. Keep the runtime stack unchanged and update only the
# affected userspace kernel headers to the exact fixed Jammy package.
RUN set -eux; \
    apt-get update; \
    apt-get install --yes --no-install-recommends linux-libc-dev=5.15.0-187.197; \
    rm -rf /var/lib/apt/lists/*; \
    test "$(dpkg-query -W -f='${Version}' linux-libc-dev)" = "5.15.0-187.197"

# MiaAI-Lab applies these compatibility and performance hotfixes to the same
# pinned Anemll image on both DGX Sparks. Fetch them from the immutable upstream
# revision, verify every byte, and bake them into the derivative image so recipe
# launches remain offline and both nodes always execute the identical runtime.
RUN set -eux; \
    patch_dir=/tmp/mialabs-patches; \
    mkdir -p "$patch_dir"; \
    base="https://raw.githubusercontent.com/MiaAI-Lab/DeepSeek-v4-Flash-DSpark-2x-DGX-Spark/${MIALABS_REVISION}/patches"; \
    python3 - "$base" "$patch_dir" <<'PY'
import pathlib
import sys
import urllib.request

base, destination = sys.argv[1], pathlib.Path(sys.argv[2])
names = (
    "hotfix-nvfp4-ds-mla-issue22.sh",
    "hotfix-dsv4-mtp-buffer-50312.sh",
    "hotfix-dsv4-adaptive-topk-50004.sh",
    "hotfix-dsv4-skip-topk-49486.sh",
    "hotfix-dsv4-dense-prefill-indexer-48407.sh",
    "hotfix-dsv4-skip-empty-c128-48957.sh",
    "hotfix-dsv4-flashmla-workspace-50298.sh",
    "hotfix-dsv4-grammar-advance.sh",
    "hotfix-dsv4-issue26-hybrid-swa-min.py",
    "hotfix-dsv4-issue27-partial-prefill-concurrency.py",
    "hotfix-dsv4-suppress-stops-in-reasoning.py",
    "hotfix-encoding-dsv4-issue21.py",
)
for name in names:
    with urllib.request.urlopen(f"{base}/{name}") as response:
        (destination / name).write_bytes(response.read())
PY
RUN set -eux; \
    cd /tmp/mialabs-patches; \
    printf '%s\n' \
      '4999ed58c4c2ca0903bc21fcdb6db50d481396ded62066e4132ea609096b13bf  hotfix-nvfp4-ds-mla-issue22.sh' \
      '18dee7b92db1c6c55983c7a9df4d6c27c5a09d9be2225cd54207837fe94ecfe0  hotfix-dsv4-mtp-buffer-50312.sh' \
      '561a6ebd295964e3a37df07c96259a1a2eb0d7e6aaef5ac5ca73ecb0cebf7493  hotfix-dsv4-adaptive-topk-50004.sh' \
      '636fd162fefc2a156750027b731a9eb136e7993f2552389adf7e3647c5b4dc7b  hotfix-dsv4-skip-topk-49486.sh' \
      'c2fa444ea40af9225f3063b3be3a5827f4cada9b0ddf84e156176a23e99a2e6b  hotfix-dsv4-dense-prefill-indexer-48407.sh' \
      'dabafb64f9273c37659027706920d175d5ed0a6b0cdd53fb5be784f408d7990e  hotfix-dsv4-skip-empty-c128-48957.sh' \
      'a7f557b264d247fbc65bfe49cc6d05e0780e4c6bebcdaf3633ace55338fa4268  hotfix-dsv4-flashmla-workspace-50298.sh' \
      '99f5e0d3737a8a074c4c85b7348882a91a4d96a12bcf0d65de4d1c751a4d8abd  hotfix-dsv4-grammar-advance.sh' \
      'acdf9aa2705de248333b3ba6ddeb20aea67b5582f408552e407c7a670b20ee82  hotfix-dsv4-issue26-hybrid-swa-min.py' \
      'e87e14a6dc45ccbbdea2940d9594f239f6d8dbda7b82d7a094f45bcaa2dfb450  hotfix-dsv4-issue27-partial-prefill-concurrency.py' \
      '89df901d5d5853e79d71d48e1f2f1a4302ac688b5e2d3788c8551a7fe8477f21  hotfix-dsv4-suppress-stops-in-reasoning.py' \
      '1a74f6c4ec6a2b7cd2ff01f19b52fbf4ced980a22f08b9d75a6aae1bff0d0548  hotfix-encoding-dsv4-issue21.py' \
      | sha256sum --check --strict; \
    bash hotfix-nvfp4-ds-mla-issue22.sh; \
    bash hotfix-dsv4-mtp-buffer-50312.sh; \
    bash hotfix-dsv4-adaptive-topk-50004.sh; \
    bash hotfix-dsv4-skip-topk-49486.sh; \
    bash hotfix-dsv4-dense-prefill-indexer-48407.sh; \
    bash hotfix-dsv4-skip-empty-c128-48957.sh; \
    bash hotfix-dsv4-flashmla-workspace-50298.sh; \
    bash hotfix-dsv4-grammar-advance.sh; \
    python3 hotfix-dsv4-issue27-partial-prefill-concurrency.py; \
    python3 hotfix-dsv4-issue26-hybrid-swa-min.py; \
    python3 hotfix-dsv4-suppress-stops-in-reasoning.py; \
    test ! -e /usr/local/lib/python3.12/dist-packages/vllm/v1/worker/gpu/sample/thinking_budget.py; \
    grep -q 'thinking_token_budget is not yet supported by the V2' /usr/local/lib/python3.12/dist-packages/vllm/v1/engine/input_processor.py; \
    install -m 0555 hotfix-encoding-dsv4-issue21.py /opt/cloudless-hotfix-encoding-dsv4-issue21.py; \
    rm -rf "$PWD"

LABEL org.opencontainers.image.title="Cloudless MiaLabs DeepSeek V4 Flash DSpark 2x runtime" \
      org.opencontainers.image.description="Pinned Anemll DSpark image with MiaAI-Lab's two-DGX-Spark compatibility and performance hotfixes" \
      org.opencontainers.image.source="https://github.com/BeCloudless-AI/cloudlessos" \
      ai.becloudless.base.digest="sha256:a83948492cf13df455170fb42885f5ef4db54fefe0feff0f841ecbff464ac9d8" \
      ai.becloudless.linux-libc-dev.version="5.15.0-187.197" \
      ai.becloudless.upstream.url="https://github.com/MiaAI-Lab/DeepSeek-v4-Flash-DSpark-2x-DGX-Spark" \
      ai.becloudless.upstream.revision="3c9576c52ab71d89e22fe4621e0d32300a59039a"
