# Docker Deployment

> Back to [README](../../README.en.md)

GoGoClaw production deployments use the container image only. Native binaries and source builds are for development and testing. This guide covers Compose, direct Docker on a NAS, first-run setup, updates, backups, and common deployment failures.

## Image And Ports

```text
ghcr.io/biaobiaobiao108/gogoclaw:latest
```

Supported platforms are Linux `amd64` and `arm64`. The image contains:

- `gogoclaw`: CLI and gateway
- `gogoclaw-launcher`: web console and gateway supervisor

| Port | Purpose |
|---|---|
| `18800` | Web console |
| `18790` | Gateway health checks and channel services |

Telegram, WeChat, Discord, Feishu, and QQ make outbound connections. A normal deployment only needs to publish `18800`.

## Requirements

- Docker Engine
- Docker Compose v2, optional
- 64-bit Linux on `amd64` or `arm64`
- Persistent storage for `/data`
- Outbound access to model APIs and chat platforms; use a proxy when required
- A TLS reverse proxy when exposing the console beyond a trusted LAN

## Docker Compose

```bash
git clone https://github.com/biaobiaobiao108/gogoclaw.git
cd gogoclaw

docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

The default URL is `http://127.0.0.1:18800`. For access from another device on a NAS or LAN:

```bash
GOGOCLAW_LAUNCHER_BIND=0.0.0.0 \
  docker compose -f docker/docker-compose.yml --profile launcher up -d
```

Compose uses the `gogoclaw-data` named volume and is the recommended first deployment.

## Direct Docker Run On A NAS

### 1. Create The Data Directory

The image runs as UID/GID `10001`. A host bind mount must be owned by that identity:

```bash
sudo mkdir -p /vol1/1000/docker/gogoclaw
sudo chown -R 10001:10001 /vol1/1000/docker/gogoclaw
sudo chmod 750 /vol1/1000/docker/gogoclaw
```

### 2. Start The Container

```bash
sudo docker run -d \
  --name gogoclaw \
  --restart unless-stopped \
  --user 10001:10001 \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=256m \
  -e TZ=Asia/Shanghai \
  -e GOGOCLAW_GATEWAY_HOST=0.0.0.0 \
  -p 18800:18800 \
  -v /vol1/1000/docker/gogoclaw:/data \
  --health-cmd 'curl -fsS http://127.0.0.1:18800/api/auth/status >/dev/null || exit 1' \
  --health-interval 30s \
  --health-timeout 3s \
  --health-retries 3 \
  ghcr.io/biaobiaobiao108/gogoclaw:latest \
  gogoclaw-launcher -public -no-browser
```

Open `http://<NAS-IP>:18800`.

### 3. First-Run Setup

1. Create the console password. An empty `/data` has no default password; reused data keeps its existing password.
2. Add at least one model and set it as the default.
3. Configure channels.
4. Check gateway and channel status.
5. Send a test message from Web or a connected chat platform.

The launcher supervises the gateway. Do not start a separate `gogoclaw-gateway` container against the same `/data`.

## Credentials And Encryption Passphrase

Sensitive values are stored in `/data/.gogoclaw/.security.yml`. If credential encryption is enabled, provide `GOGOCLAW_KEY_PASSPHRASE`:

```bash
-e GOGOCLAW_KEY_PASSPHRASE='your-strong-passphrase'
```

Do not put the passphrase in the image, repository, or a public Compose file. Back it up separately; encrypted credentials cannot be recovered without it.

## Model Configuration

The web console exposes four canonical protocols:

| Provider | Protocol |
|---|---|
| `openai` | OpenAI Chat Completions |
| `openai-responses` | OpenAI Responses API |
| `anthropic-messages` | Anthropic Messages API |
| `gemini` | Google Gemini generateContent API |

For OpenAI Chat Completions-compatible services, select `openai` and set `api_base`. When reaching a model proxy on the Docker host, Linux commonly requires `http://172.17.0.1:<port>` rather than `localhost`, which refers to the container itself.

At least one model must be configured as the default before ordinary channel messages can reach the agent.

## Common Deployment Failures

### Container Starts But Cannot Save Configuration

Check host directory ownership:

```bash
sudo chown -R 10001:10001 /vol1/1000/docker/gogoclaw
```

### LAN Cannot Open The Console

- Start the launcher with `-public`.
- Publish `18800:18800`.
- Check the NAS firewall and reverse proxy.
- A reverse proxy must allow WebSocket upgrades because Pico chat uses WebSocket.

If the reverse proxy cannot preserve the external port or protocol, declare the browser-visible address and trust the proxy source in `launcher-config.json`:

```json
{
  "port": 18800,
  "public": true,
  "trusted_proxy_cidrs": ["192.168.31.228/32"],
  "public_url": "http://gogoclaw.example.com:666"
}
```

Trusted proxy requests use `public_url` for origin checks, WebSocket/SSE URLs, secure cookies, OAuth callbacks, and HSTS. Direct LAN/local requests keep their own origin. Restart the launcher after changing it.

### Password Page Appears Although You Never Set One

The console password is stored in `/data`, not an environment variable. This usually means an old directory or named volume is mounted. Use the old password for retained data or an empty `/data` for a fresh installation.

### Telegram Does Not Reply

Put the proxy in `settings.proxy`:

```json
{
  "settings": {
    "base_url": "https://api.telegram.org",
    "proxy": "http://user:password@192.168.1.10:7890"
  }
}
```

Putting the proxy URL in `base_url` causes `decode json: unexpected end of JSON input`, and the bot never receives updates.

### WeChat Binding Succeeds But There Is No Reply

`allow_from` must contain the real iLink `from_user_id`, not a WeChat nickname. New web bindings automatically write the returned user ID. Accounts bound before this behavior was added should be rebound once.

Temporarily set `gateway.log_level` to `debug`, then send a new message. A rejection looks like:

```text
Message rejected by allowlist  from_user_id=o9cq...@im.wechat
```

Put that exact value into `allow_from`, restart the gateway, and restore `warn` logging afterward.

### ARM NAS Pull Is Slow Or Fails

```bash
uname -m
```

`aarch64` uses the `arm64` image. The first pull may download large multi-platform layers. Configure a reliable container registry mirror or network proxy when GHCR access is unstable.

## Logs And Health Checks

```bash
sudo docker logs --tail 200 -f gogoclaw
sudo docker exec gogoclaw tail -n 200 /data/.gogoclaw/logs/gateway.log

curl -fsS http://127.0.0.1:18800/api/auth/status
sudo docker exec gogoclaw curl -fsS http://127.0.0.1:18790/health
sudo docker exec gogoclaw curl -fsS http://127.0.0.1:18790/ready
```

## Upgrade

```bash
sudo docker pull ghcr.io/biaobiaobiao108/gogoclaw:latest
sudo docker rm -f gogoclaw
# Recreate with the same docker run arguments; /data is preserved
```

Compose:

```bash
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

## Backup And Restore

Stop every container writing to the same volume before backing up, including the launcher-managed gateway, standalone gateways, and one-shot agents:

```bash
docker compose -f docker/docker-compose.yml --profile gateway --profile launcher --profile agent stop
```

Also stop instances launched directly with Docker or a NAS UI. A read-only mount does not stop other containers from writing. Archiving live SQLite, WAL, and JSONL files does not produce a consistent snapshot. These are offline backups; restart your original deployment after completion. Online backups require the SQLite Backup API or a consistent snapshot, not a copy of only the `.db` file.

Back up a named volume:

```bash
docker run --rm \
  -v gogoclaw-data:/data:ro \
  -v "${PWD}:/backup" \
  alpine tar czf /backup/gogoclaw-data.tgz -C /data .
```

Back up a host directory:

```bash
sudo tar czf gogoclaw-data-$(date +%F).tar.gz \
  -C /vol1/1000/docker/gogoclaw .
```

Stop the container and restore into an empty target directory. After restoring, set ownership back to `10001:10001`.

## Memory And History Retention

Compose defaults to a `2g` container memory limit and a `512MiB` `GOMEMLIMIT` per Go process, with Docker log rotation at `10m × 3`. Override these with `GOGOCLAW_MEMORY_LIMIT` and `GOGOCLAW_GO_MEMORY_LIMIT`. The launcher and its gateway share the container limit but each has its own Go budget. Leave additional room for MCP servers, model CLIs, Python, Bun, and `/tmp`. The Go soft limit does not cap the whole container; increase the container limit for heavier tools based on measured peak usage.

The `sessions.db` SQLite store keeps the complete transcript. Summarization moves only the model context offset, so history and search still include earlier messages. Deleting a session releases pages to SQLite's freelist for reuse, but the database file does not shrink immediately; GoGoClaw intentionally avoids repeated `VACUUM` operations. Back up the complete SQLite WAL set and delete unused sessions when their data is no longer needed.

Memory prompt excerpts default to approximately `2048` estimated tokens, capped at one eighth of the model's available input context. Configure `agents.defaults.memory_context_tokens` or `GOGOCLAW_AGENTS_DEFAULTS_MEMORY_CONTEXT_TOKENS`. The original `MEMORY.md` and last three days of notes remain intact and readable with file tools. Seahorse reconciles sessions on first use rather than loading every transcript during startup.

## Security Boundary

The Compose and direct-run examples use:

- UID/GID `10001`
- A read-only root filesystem
- All Linux capabilities dropped
- `no-new-privileges`
- A bounded writable `/tmp`

The container is the execution sandbox, so `tools.approval` is disabled by default. Do not mount the Docker socket or unrelated host directories.
