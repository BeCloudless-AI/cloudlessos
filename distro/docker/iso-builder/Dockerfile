FROM golang:1.26-bookworm

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        curl \
        imagemagick \
        librsvg2-bin \
        xorriso \
    && rm -rf /var/lib/apt/lists/*
