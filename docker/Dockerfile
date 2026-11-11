# Production image for the GoGoClaw gateway and web console.
FROM --platform=$BUILDPLATFORM oven/bun:1.4.4-alpine AS frontend

WORKDIR /src/web/frontend

COPY web/frontend/package.json web/frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/frontend/ ./
RUN bun run build:backend

FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.24 AS builder

ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache git make
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/backend/dist web/backend/dist

RUN GOOS=$TARGETOS GOARCH=$TARGETARCH GO_BUILD_TAGS=goolm,stdjson make build && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags goolm,stdjson \
      -ldflags "-s -w" \
      -o build/gogoclaw-launcher ./web/backend/

FROM oven/bun:1.4.4-alpine AS bun

FROM alpine:3.24.4

RUN apk add --no-cache \
      bash \
      ca-certificates \
      curl \
      git \
      jq \
      openssh-client \
      python3 \
      py3-pip \
      tini \
      tzdata && \
    addgroup -S -g 10001 bun && \
    adduser -S -D -H -u 10001 -G bun bun && \
    mkdir -p /data && \
    chown -R bun:bun /data

COPY --from=builder /src/build/gogoclaw /usr/local/bin/gogoclaw
COPY --from=builder /src/build/gogoclaw-launcher /usr/local/bin/gogoclaw-launcher
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
COPY --from=ghcr.io/astral-sh/uv:0.13.7 /uv /uvx /usr/local/bin/
COPY docker/entrypoint.sh /usr/local/bin/gogoclaw-entrypoint
RUN ln -s /usr/local/bin/bun /usr/local/bin/bunx && \
    chmod 0755 /usr/local/bin/gogoclaw /usr/local/bin/gogoclaw-launcher /usr/local/bin/gogoclaw-entrypoint

ENV HOME=/data \
    BUN_INSTALL=/data/.bun \
    PIP_USER=1 \
    PYTHONUSERBASE=/data/.local \
    XDG_CACHE_HOME=/data/.cache \
    PATH=/data/.bun/bin:/data/.local/bin:/usr/local/bin:/usr/bin:/bin

WORKDIR /data
VOLUME ["/data"]
USER bun

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/gogoclaw-entrypoint"]
CMD ["gateway"]
