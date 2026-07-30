FROM nvcr.io/nvidia/pytorch:26.06-py3@sha256:43c018d6a12963f1a1bad85ef8574b5c2a978eec2be0ebcacfb87f69e0d210e1

ENV HF_HUB_DISABLE_TELEMETRY=1 \
    PYTHONUNBUFFERED=1

RUN python3 -m pip install --no-cache-dir \
      fastapi==0.116.1 \
      uvicorn==0.35.0 \
      python-multipart==0.0.20 \
      transformers==4.57.1 \
      peft==0.17.1 \
      opencv-python-headless==4.11.0.86 \
      Pillow==11.1.0 \
      lmdb==1.7.5 && \
    if [ "$(uname -m)" = "x86_64" ]; then python3 -m pip install --no-cache-dir decord==0.6.0; fi

COPY server.py /opt/cloudless/server.py
EXPOSE 8000
ENTRYPOINT ["python3", "/opt/cloudless/server.py"]
