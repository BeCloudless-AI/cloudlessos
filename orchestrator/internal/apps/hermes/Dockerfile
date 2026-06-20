# Cloudless build of Hermes Agent (Nous Research), pre-configured to use
# Cloudless AI (vLLM). Hermes installs via an official curl script.
FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates bash git xz-utils \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://hermes-agent.nousresearch.com/install.sh | bash

# Bake the Cloudless AI endpoint config and key.
RUN mkdir -p /root/.hermes
COPY config.yaml /root/.hermes/config.yaml
COPY hermes.env /root/.hermes/.env

# `gateway run` is the foreground mode (no systemd, correct for containers).
CMD ["hermes", "gateway", "run"]
