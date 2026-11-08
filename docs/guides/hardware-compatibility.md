# GoGoClaw Container Host Compatibility

Production GoGoClaw runs only from the published Linux container image. Native binaries and source builds are intended for development and testing.

## Supported Image Platforms

| Platform | Status | Host requirements |
|----------|--------|-------------------|
| `linux/amd64` | Supported | 64-bit x86 Linux host with a Docker or OCI runtime |
| `linux/arm64` | Supported | 64-bit ARM Linux host with a Docker or OCI runtime |
Only `amd64` and `arm64` release images are published. Windows and macOS are supported only as development environments; production containers must run on Linux.

## Host Requirements

- Docker Engine with BuildKit, or another compatible OCI runtime
- Docker Compose v2 when using the provided `docker/docker-compose.yml`
- At least 512MB RAM available to the container; 1GB or more recommended
- At least 1GB free storage for the image, workspace, and runtime caches
- Outbound network access for model providers and chat platforms
- TLS termination in a reverse proxy before exposing the Web console or webhook endpoint outside the local host

The image runs as UID/GID `10001`. `/data` must be a persistent writable volume; it stores configuration, credentials, workspace files, sessions, and logs.

## Deployment

Use the [Docker deployment guide](docker.md) for installation, backup, upgrade, and security guidance.
