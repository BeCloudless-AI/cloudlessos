FROM nvcr.io/nvidia/nemo-rl:v0.7.0@sha256:75c4821c7f24d6a045d22ce51756bd7ea9ec208020c90233723b9d7a9727d681

RUN python -m pip install --no-cache-dir jupyterlab==4.6.2 && \
    git clone --branch v0.7.0 --depth 1 --recurse-submodules --shallow-submodules \
      https://github.com/NVIDIA-NeMo/RL.git /workspace/nemo-rl

WORKDIR /workspace
EXPOSE 8891
ENTRYPOINT ["python", "-m", "jupyterlab"]
