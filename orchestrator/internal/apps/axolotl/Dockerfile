FROM axolotlai/axolotl-uv:main-py3.12-cu130-2.12.0@sha256:4e631d63359d15600f4de051dbd8100d655da59bc4ae37f25037e8efebe630e4

RUN python -m pip install --no-cache-dir jupyterlab==4.6.2 && \
    git clone --branch v0.9.2 --depth 1 \
      https://github.com/axolotl-ai-cloud/axolotl.git /workspace/axolotl-examples

WORKDIR /workspace
EXPOSE 8894
ENTRYPOINT ["python", "-m", "jupyterlab"]
