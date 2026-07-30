FROM python:3.12-slim@sha256:57cd7c3a7a273101a6485ba99423ee568157882804b1124b4dd04266317710de

RUN apt-get update && apt-get install -y --no-install-recommends git && \
    rm -rf /var/lib/apt/lists/* && \
    python -m pip install --no-cache-dir \
      data-designer==0.8.0 \
      jupyterlab==4.6.2 \
      ipykernel==7.1.0

COPY README.md /workspace/README.md
COPY getting-started.ipynb /workspace/getting-started.ipynb
WORKDIR /workspace
EXPOSE 8892
ENTRYPOINT ["python", "-m", "jupyterlab"]
