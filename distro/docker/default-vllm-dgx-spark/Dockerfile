FROM nvcr.io/nvidia/vllm@sha256:1de8e6bfdb4c81c1f31a806cc9b13b5c6352714a7cec87f4d24964bcc91159b2

# NVIDIA vLLM 26.07 contains vLLM 0.24 but xgrammar 0.2.0. Tool-enabled
# requests import normalize_tool_choice, which was added after xgrammar 0.2.0,
# so the stock image returns HTTP 500 while its basic /v1/models probe passes.
ARG XGRAMMAR_VERSION=0.2.4
ARG XGRAMMAR_WHEEL=xgrammar-0.2.4-cp312-cp312-manylinux_2_26_aarch64.manylinux_2_28_aarch64.whl
ARG XGRAMMAR_SHA256=b1d2bef13aec8122dfa6b96e3ece2cc0a42a732685f1d44f6248c305274e8eae

RUN set -eux; \
    python3 -m pip download --only-binary=:all: --no-deps --dest /tmp "xgrammar==${XGRAMMAR_VERSION}"; \
    echo "${XGRAMMAR_SHA256}  /tmp/${XGRAMMAR_WHEEL}" | sha256sum --check --strict; \
    python3 -m pip install --no-cache-dir --no-deps "/tmp/${XGRAMMAR_WHEEL}"; \
    python3 -c "from xgrammar import StructuralTag, normalize_tool_choice"; \
    rm -f "/tmp/${XGRAMMAR_WHEEL}"

LABEL org.opencontainers.image.title="Cloudless vLLM for DGX Spark" \
      org.opencontainers.image.description="NVIDIA vLLM 26.07 with a compatible pinned xgrammar runtime" \
      org.opencontainers.image.source="https://github.com/samuelcardillo/Cloudless"
