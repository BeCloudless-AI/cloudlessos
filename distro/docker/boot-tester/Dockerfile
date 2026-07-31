FROM ubuntu:24.04

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        imagemagick \
        ovmf \
        qemu-system-x86 \
        socat \
        xorriso \
    && rm -rf /var/lib/apt/lists/*
