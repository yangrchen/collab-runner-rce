FROM ghcr.io/astral-sh/uv:0.5.29-python3.12-bookworm

RUN apt-get update && \
    apt-get install -y init

COPY ./state-parser /app
ENV UV_PROJECT_ENVIRONMENT=/usr/local

WORKDIR /app
RUN uv sync --frozen