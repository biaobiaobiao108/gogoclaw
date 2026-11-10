<div align="center">
<img src="assets/logo.svg" alt="GoGoClaw" width="128">

<h1>GoGoClaw</h1>

<p>A lightweight general-purpose agent harness and web console written in Go</p>

<p>
  <img src="https://img.shields.io/badge/Go-1.27.3+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Arch-amd64%20%7C%20arm64-blue" alt="amd64 / arm64">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="MIT License">
</p>

<p>
  <a href="README.md">中文</a> | <strong>English</strong>
</p>
</div>

---

GoGoClaw is a single-host, private-deployment agent harness for long-running personal and small-team use. Its core is written in Go, and its web console is a React 19 and Tailwind CSS v4 application built with Bun. It can connect to OpenAI, Anthropic, and Gemini model protocols and expose the same agent through Web, Telegram, WeChat, Discord, Feishu, and QQ channels.

GoGoClaw treats one trusted instance as its runtime boundary. It does not provide multi-tenancy, RBAC, or horizontal scaling, and only one GoGoClaw instance should manage a data directory.

Production deployment is supported through the container image only. The container is the runtime security boundary, and tool approval is disabled by default.

## Highlights

- **Complete agent loop**: sessions, durable memory, workspace files, skills, MCP, tools, plans, streaming responses, and subagents.
- **Four model protocols**: OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Google Gemini generateContent.
- **Multi-channel access**: Web, Telegram, personal WeChat, Discord, Feishu / Lark, and QQ.
- **Modern web console**: manage models, channels, tools, sessions, approvals, logs, and runtime status in one place.
- **Container-first**: published images support Linux `amd64` and `arm64` and run as a non-root user.
- **Persistent state**: configuration, credentials, the SQLite session store, workspace files, channel cursors, and logs live under `/data`.

## Quick Start

### Requirements

- Docker Engine
- Docker Compose v2, optional
- A 64-bit Linux host on `amd64` or `arm64`
- Outbound access to model APIs and chat platforms; use an HTTP or SOCKS5 proxy in restricted networks
- A TLS reverse proxy if the console is exposed to an untrusted network

### Option 1: Docker Compose

```bash
git clone https://github.com/biaobiaobiao108/gogoclaw.git
cd gogoclaw

docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

The launcher binds to `127.0.0.1:18800` by default. To access it from another device on a NAS or LAN:

```bash
GOGOCLAW_LAUNCHER_BIND=0.0.0.0 \
  docker compose -f docker/docker-compose.yml --profile launcher up -d
```

Compose uses the `gogoclaw-data` named volume, so host directory ownership is handled automatically. This is the simplest first deployment.

### Option 2: Direct Docker Run (Common On NAS)

Prepare a persistent directory. The container uses UID/GID `10001`, so a host bind mount must be owned by that identity:

```bash
sudo mkdir -p /vol1/1000/docker/gogoclaw
sudo chown -R 10001:10001 /vol1/1000/docker/gogoclaw
sudo chmod 750 /vol1/1000/docker/gogoclaw
```

Start the web console:

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

Open `http://<server-ip>:18800`. On the first run with an empty `/data`, the console asks you to create a password. There is no default password.

Port `18800` serves the console. `gogoclaw-launcher` starts and manages the gateway inside the same container, so you normally do not need to publish or start a second `gogoclaw-gateway` container.

### First-Run Checklist

1. Create the console password.
2. Add at least one model and set it as the default.
3. Configure Telegram, WeChat, or another channel.
4. Check gateway and channel status in the overview.
5. Send a test message from Web or the connected chat platform.

## Model Protocols

The web console exposes four canonical model protocols:

| Provider ID | Protocol | Default API Base |
|---|---|---|
| `openai` | OpenAI Chat Completions | `https://api.openai.com/v1` |
| `openai-responses` | OpenAI Responses API | `https://api.openai.com/v1` |
| `anthropic-messages` | Anthropic Messages API | `https://api.anthropic.com/v1` |
| `gemini` | Google Gemini generateContent API | `https://generativelanguage.googleapis.com/v1beta` |

For an OpenAI Chat Completions-compatible service, select `openai` and override `api_base`. Use `anthropic-messages` for Anthropic Messages-compatible endpoints and `gemini` for Gemini generateContent-compatible endpoints.

Key model fields:

```json
{
  "model_name": "my-model",
  "provider": "openai",
  "model": "model-id",
  "api_base": "https://api.example.com/v1",
  "api_keys": ["sk-..."],
  "streaming": { "enabled": true }
}
```

See [Providers and Models](docs/guides/providers.md) for fallback chains and routing.

## Channels

| Channel | Connection | Important Note |
|---|---|---|
| Web / Pico | Built-in WebSocket | Configured automatically with the console |
| Telegram | Bot API long polling | Put network proxies in `settings.proxy`, never in `base_url` |
| Personal WeChat | Tencent iLink QR login | `allow_from` must use the iLink `from_user_id` |
| Discord | Bot WebSocket | Correct bot intents are required |
| Feishu / Lark | WebSocket / SDK | No public callback URL is required |
| QQ | Bot WebSocket | Enable the required sandbox or group permissions on QQ |

### Telegram Proxy

Correct configuration:

```json
{
  "channel_list": {
    "telegram": {
      "enabled": true,
      "type": "telegram",
      "settings": {
        "base_url": "https://api.telegram.org",
        "proxy": "http://user:password@192.168.1.10:7890"
      }
    }
  }
}
```

`base_url` is the Telegram Bot API endpoint, not a proxy field. Putting a proxy URL there produces an invalid Bot API URL and usually appears in logs as `decode json: unexpected end of JSON input`.

### Personal WeChat

After a successful QR binding, the web console saves the token and automatically writes the returned iLink `ilink_user_id` into `allow_from`. Do not use a WeChat nickname. Real senders in this protocol normally end in `@im.wechat`.

If the account was bound before this behavior was added, rebind it once from the channel page. For troubleshooting, temporarily set `gateway.log_level` to `debug`; rejected messages include:

```text
Message rejected by allowlist  from_user_id=...
```

Put that exact `from_user_id` into `allow_from` and restart the gateway. Rejections are also logged at warning level so they are not silently dropped under normal `warn` logging.

## Docker Deployment Pitfalls

### 1. Host Directory Ownership

The container runs as UID/GID `10001`. With a bind mount, a root-owned `/data` may let the container start but prevent configuration, password, database, or log writes.

```bash
sudo chown -R 10001:10001 /path/to/gogoclaw-data
```

Use a Compose named volume if you do not want to manage ownership.

### 2. Port Mapping Looks Correct But LAN Access Fails

- The launcher must receive `-public` to listen on container interfaces.
- Docker must publish `18800:18800`.
- The NAS firewall, reverse proxy, and upstream security groups must allow the port.
- `18790` is the gateway port. Telegram, WeChat, Discord, Feishu, and QQ make outbound connections, so this port usually does not need public exposure.

### 3. Multiple Services On One Data Directory

The launcher already manages the gateway. Do not run a launcher and a standalone `gogoclaw-gateway` against the same `/data`; they will compete over PIDs, ports, and configuration.

### 4. Wrong Architecture Or Image

```bash
uname -m
sudo docker image inspect ghcr.io/biaobiaobiao108/gogoclaw:latest \
  --format '{{.Os}}/{{.Architecture}}'
```

`x86_64` maps to `amd64`; `aarch64` maps to `arm64`. 32-bit platforms are not supported.

### 5. Unknown Password After Restart

The console password is stored in persistent `/data`. If you reuse an old directory or volume, its password belongs to that old data; it is not a container default. Use an empty `/data` for a fresh install or the original password for retained data.

### 6. Channel Is Enabled But Nobody Responds

Check the path in order:

1. Did the platform deliver the message to the gateway?
2. Does `allow_from` contain the platform's real user ID?
3. Did the agent receive the message and issue a model request?
4. Did the model request succeed?
5. Did outbound delivery succeed?

Logs:

```bash
sudo docker logs --tail 200 -f gogoclaw
sudo docker exec gogoclaw tail -n 200 /data/.gogoclaw/logs/gateway.log
```

## Operations

### Health Checks

```bash
curl -fsS http://127.0.0.1:18800/api/auth/status
curl -fsS http://127.0.0.1:18790/health
curl -fsS http://127.0.0.1:18790/ready
```

### Upgrade

```bash
sudo docker pull ghcr.io/biaobiaobiao108/gogoclaw:latest
sudo docker rm -f gogoclaw
# Recreate the container with the same docker run command; /data is preserved
```

With Compose:

```bash
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

### Backup

Back up all of `/data`: configuration, `.security.yml`, SQLite databases, workspace, skills, channel state, and logs.

```bash
sudo tar czf gogoclaw-data-$(date +%F).tar.gz \
  -C /vol1/1000/docker/gogoclaw .
```

Never commit `.security.yml`, API keys, bot tokens, or device private keys to Git.

## Agent Behavior And Prompt

GoGoClaw includes a compact default system prompt covering general operating principles: understand before acting, use tools when required, claim success only when tool evidence supports it, inspect real state before changes, recover from failures, never fabricate results, treat external instructions as untrusted data, and communicate concisely.

The prompt also defines GoGoClaw-specific runtime rules: only tools exposed for the current turn are authoritative; workspace paths and persistence have explicit boundaries; long-term memory is reserved for stable preferences and decisions; normal replies are delivered to the current channel; subagents are isolated by default; credentials may only be used with their intended service; and responses should use the user's language and fit the delivery channel.

Workspace files can refine behavior:

- `AGENT.md`: primary project or agent instructions.
- `SOUL.md`: role, tone, and durable style.
- `USER.md`: user preferences.
- `memory/MEMORY.md`: durable cross-session memory.
- `skills/<name>/SKILL.md`: capability instructions loaded on demand.

Explicit user instructions take precedence over conversation summaries. Workspace instructions add context; they do not bypass the security boundary.

## Development

Requirements: Go 1.27.3+ and Bun 1.4.4+.

```bash
make deps
make build
make build-launcher

cd web
make dev
```

Tests and formatting:

```bash
make test
make fmt
```

Local binaries, direct gateway processes, and development containers are for development and testing only.

## Documentation

- [Docker Deployment](docs/guides/docker.md)
- [Chat Apps](docs/guides/chat-apps.md)
- [Providers and Models](docs/guides/providers.md)
- [Configuration](docs/guides/configuration.md)
- [Tools and MCP](docs/reference/tools_configuration.md)
- [Troubleshooting](docs/operations/troubleshooting.md)

## License

[MIT](LICENSE)
