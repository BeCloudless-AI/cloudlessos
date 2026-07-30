FROM hijkzzz/molt:0.1.4@sha256:0f4d9b59de4a6d7730c854dd4329e90c1684822d40c497ed8ea191b3a3fe6f6e

RUN python -m pip install --no-cache-dir jupyterlab==4.6.2

WORKDIR /molt
EXPOSE 8893
ENTRYPOINT ["python", "-m", "jupyterlab"]
