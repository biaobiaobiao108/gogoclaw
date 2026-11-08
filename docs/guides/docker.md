# Docker Deployment

> Back to [README](../../README.md)

PicoClaw production deployments use the container image only. Native binaries and source builds are for development and testing.

## Prerequisites

- Docker Engine with Compose v2
- A persistent volume for `/data`
- TLS termination in a reverse proxy before exposing the Web console or webhook endpoint outside the local host

The published image contains both `picoclaw` and `picoclaw-launcher`. The Compose file uses one persistent volume, `picoclaw-data`.

## Web Console

```bash
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

Open `http://127.0.0.1:18800`, create the dashboard password, then configure models, channels, and tools. The launcher creates the initial config and manages the gateway process in the same container.

The host port binds to loopback by default. Keep it private, or put a hardened TLS reverse proxy in front of it:

```bash
PICOCLAW_LAUNCHER_BIND=0.0.0.0 \
  docker compose -f docker/docker-compose.yml --profile launcher up -d
```

Run `config.json` and the Web console over HTTPS whenever they cross an untrusted network.

## Headless Gateway

Create the initial configuration:

```bash
docker compose -f docker/docker-compose.yml --profile gateway pull
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint picoclaw picoclaw-gateway onboard
```

Start the gateway:

```bash
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

Webhook-capable channels use port `18790`. The host binding defaults to `127.0.0.1`. Set `PICOCLAW_GATEWAY_BIND` only for a reverse proxy or trusted private network.

## One-Off Commands

```bash
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint picoclaw picoclaw-gateway agent -m "What is 2+2?"

docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint picoclaw picoclaw-gateway status
```

## Data And Updates

The volume is mounted at `/data`. It contains `/data/.picoclaw/config.json`, credentials, workspace files, sessions, and logs.

```bash
# Backup
docker run --rm \
  -v picoclaw-data:/data:ro \
  -v "${PWD}:/backup" \
  alpine tar czf /backup/picoclaw-data.tgz -C /data .

# Restore into an empty volume
docker run --rm \
  -v picoclaw-data:/data \
  -v "${PWD}:/backup" \
  alpine tar xzf /backup/picoclaw-data.tgz -C /data

# Update
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

The project has no in-application self-update command.

## Security Boundary

The Compose service runs as UID/GID `10001`, uses a read-only root filesystem, drops all capabilities, sets `no-new-privileges`, and provides a bounded writable `/tmp`.

The workspace path check reduces accidental writes; it is not an operating-system sandbox. The container is the production boundary. Do not mount the Docker socket or unrelated host paths.

## Development

Local builds and direct process execution are development-only:

```bash
make build
make build-launcher
cd web && make dev
```

For a local container build, use the development override:

```bash
docker compose -f docker/docker-compose.yml \
  -f docker/docker-compose.dev.yml --profile gateway up --build
```
