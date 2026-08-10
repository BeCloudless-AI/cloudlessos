# syntax=docker/dockerfile:1.7
FROM ghcr.io/0xsero/deepseek-v4-flash-0731-spark-sparkinfer@sha256:2e077489a83a0360952828051fe7f7a32c1801e5ce8436d85f7267583d614ff4

# The pinned vLLM source requires XGrammar >= 0.2.1, but the upstream image
# inherits an older XGrammar from nvcr.io/nvidia/vllm:26.02-py3. Keep the
# correction isolated to the runtime venv and prove the required API exists
# while the image is being built.
RUN env -u PIP_CONSTRAINT /opt/runtime-venv/bin/python -m pip install \
      --no-cache-dir --upgrade 'xgrammar==0.2.1' && \
    /opt/runtime-venv/bin/python -c \
      "import importlib.metadata as m; from xgrammar import normalize_tool_choice; assert m.version('xgrammar') == '0.2.1'"

LABEL org.opencontainers.image.title="Cloudless DeepSeek V4 Flash SparkInfer XGrammar compatibility image" \
      org.opencontainers.image.description="Upstream one-DGX-Spark SparkInfer image with the vLLM-required XGrammar 0.2.1 runtime dependency" \
      org.opencontainers.image.source="https://github.com/BeCloudless-AI/cloudlessos" \
      ai.becloudless.base.digest="sha256:2e077489a83a0360952828051fe7f7a32c1801e5ce8436d85f7267583d614ff4" \
      ai.becloudless.xgrammar.version="0.2.1"
