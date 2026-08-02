FROM vllm/vllm-openai@sha256:2cc49b81319f7a66a33dd8bd63a7bfddae079122b33ce51989b6828a1f038c37

LABEL org.opencontainers.image.source="https://github.com/samuelcardillo/cloudlessos"
LABEL org.opencontainers.image.description="ARM64 vLLM 0.25.1 base for Laguna S 2.1 with the scanner-blocking Ubuntu linux-libc-dev vulnerabilities patched"

USER root

RUN apt-get update \
    && apt-get install --no-install-recommends --yes \
       linux-libc-dev=5.15.0-186.196 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
