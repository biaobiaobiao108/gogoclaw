<div align="center">
<img src="assets/logo.svg" alt="GoGoClaw" width="128">

<h1>GoGoClaw: Ultra-Efficient AI Assistant in Go</h1>

<h3>Container-first agent harness · amd64 / arm64</h3>
  <p>
    <img src="https://img.shields.io/badge/Go-1.27.3+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Arch-x86__64%2C%20ARM64-blue" alt="Hardware">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
    <br>
    <a href="https://github.com/biaobiaobiao108/gogoclaw"><img src="https://img.shields.io/badge/Repository-GitHub-181717?style=flat&logo=github&logoColor=white" alt="Repository"></a>
    <a href="docs/README.md"><img src="https://img.shields.io/badge/Docs-Local-007acc?style=flat&logo=read-the-docs&logoColor=white" alt="Docs"></a>
  </p>

[中文](docs/project/README.zh.md) | **English**

</div>

---

**GoGoClaw** is a lightweight agent harness and Web control console written in **Go**, with a React dashboard for sessions, models, channels, tools, approvals, and runtime diagnostics.

**Container-first for production** — published images target 64-bit x86 and ARM Linux. Native binaries are for development and testing only.

> [!CAUTION]
> **Security Notice**
>
> * Production deployment is supported through the container image only.
> * The container is the execution sandbox. Tool approval is therefore disabled by default; enable `tools.approval` explicitly when human review is required.
> * Review the security guidance before exposing the console to a public network.

## ✨ Features

🪶 **Lean Go core**: The agent runs as a Go process without a Node.js or Python application runtime; production capacity comes from the container host.


🌍 **Container-first**: Production images are published for x86-64 and ARM64 Linux.

🔌 **MCP support**: Native [Model Context Protocol](https://modelcontextprotocol.io/) integration — connect any MCP server to extend Agent capabilities.

👁️ **Vision pipeline**: Send images and files directly to the Agent — automatic base64 encoding for multimodal LLMs.

🧠 **Smart routing**: Rule-based model routing — simple queries go to lightweight models, saving API costs.

> **[Hardware Compatibility List](docs/guides/hardware-compatibility.md)** — See tested Linux hosts that can run the container image.

### 🐜 Container Hosts

Production runs on 64-bit Linux hosts for `amd64` or `arm64` containers. See the [Hardware Compatibility List](docs/guides/hardware-compatibility.md) for host requirements.

## 📦 Production Deployment (Containers Only)

Production deployments are supported only through the published Linux container image. Native binaries built from source are intended for development and testing.

### Requirements

- Docker Engine with Docker Compose v2
- A persistent Docker volume for `/data`
- A reverse proxy with TLS if the Web console or webhook endpoint is exposed outside the local host
- Outbound network access for model providers and chat platforms

### Start With The Web Console

```bash
git clone https://github.com/biaobiaobiao108/gogoclaw.git
cd gogoclaw

docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

Open `http://127.0.0.1:18800`, create the dashboard password, then configure a model, channels, and tools. The launcher creates the initial config on first use and manages the gateway process inside the same container.

The launcher port is bound to loopback by default. Keep it private, or place a hardened reverse proxy in front of it:

```bash
# Only do this behind a TLS reverse proxy or private network.
GOGOCLAW_LAUNCHER_BIND=0.0.0.0 \
  docker compose -f docker/docker-compose.yml --profile launcher up -d
```

### Headless Gateway

Create the initial configuration interactively:

```bash
docker compose -f docker/docker-compose.yml --profile gateway pull
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint gogoclaw gogoclaw-gateway onboard
```

Then start the gateway:

```bash
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

Webhook-capable channels use gateway port `18790`. The host binding defaults to `127.0.0.1`; change `GOGOCLAW_GATEWAY_BIND` only when a reverse proxy or trusted network needs access.

### One-Off CLI Commands

```bash
# Ask a one-off question
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint gogoclaw gogoclaw-gateway agent -m "What is 2+2?"

# Inspect status
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint gogoclaw gogoclaw-gateway status
```

### Data, Backup, And Updates

Application state is stored in the named volume `gogoclaw-data` and mounted at `/data`. This includes `/data/.gogoclaw/config.json`, credentials, workspace files, sessions, and logs.

```bash
# Back up the volume
docker run --rm \
  -v gogoclaw-data:/data:ro \
  -v "${PWD}:/backup" \
  alpine tar czf /backup/gogoclaw-data.tgz -C /data .

# Restore into an empty volume (the volume must not contain existing state)
docker run --rm \
  -v gogoclaw-data:/data \
  -v "${PWD}:/backup" \
  alpine tar xzf /backup/gogoclaw-data.tgz -C /data
```

Update by pulling the new image and recreating the service:

```bash
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

There is no in-application self-update path.

### Container Security Model

The production image runs as UID/GID `10001`, uses `tini` as PID 1, and is paired with a Compose configuration that enables:

- a read-only root filesystem
- all Linux capabilities dropped
- `no-new-privileges`
- a bounded writable `/tmp`
- loopback-only host port bindings by default

The workspace path check prevents accidental writes outside the configured workspace, but it is not an operating-system sandbox. Treat the container as the production security boundary and do not mount the Docker socket or unnecessary host paths into it.

### Development Builds

Local builds are for development and testing only. Prerequisites:

- Go 1.27.3+
- Bun 1.4.4+
- PowerShell 7 on Windows

```bash
make deps
make build
make build-launcher

# Full launcher development workflow
cd web
make dev
```

Local browser launch, `-public`, and direct gateway runs are development conveniences. They are not supported production deployment methods.

To test the container build locally, use the development override:

```bash
docker compose -f docker/docker-compose.yml \
  -f docker/docker-compose.dev.yml --profile launcher up --build
```

## 🔌 Providers (LLM)

GoGoClaw supports 30+ LLM providers through the `model_list` configuration. Use the `protocol/model` format:

| Provider | Protocol | API Key | Notes |
|----------|----------|---------|-------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | Required | GPT-5.4, GPT-4o, o3, etc. |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | Required | Claude Opus 4.6, Sonnet 4.6, etc. |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | Required | Gemini 3 Flash, 2.5 Pro, etc. |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | Required | 200+ models, unified API |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | Required | GLM-4.7, GLM-5, etc. |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | Required | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | Required | Doubao, Ark models |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | Required | Qwen3, Qwen-Max, etc. |
| [Groq](https://console.groq.com/keys) | `groq/` | Required | Fast inference (Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | Required | Kimi models |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | Required | MiniMax models |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | Required | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | Required | NVIDIA hosted models |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | Required | Fast inference |
| [NEAR AI Cloud](https://near.ai/) | `nearai/` | Required | TEE inference, OpenAI-compatible |
| [Novita AI](https://novita.ai/) | `novita/` | Required | Various open models |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | Required | MiMo models |
| [Ollama](https://ollama.com/) | `ollama/` | Not needed | Local models, self-hosted |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | Not needed | Local deployment, OpenAI-compatible |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | Varies | Proxy for 100+ providers |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | API key or Entra ID** | Enterprise Azure deployment |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | Device code login |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |
| [AWS Bedrock](https://console.aws.amazon.com/bedrock)* | `bedrock/` | AWS credentials | Claude, Llama, Mistral on AWS |

> \* AWS Bedrock requires build tag: `go build -tags bedrock`. Set `api_base` to a region name (e.g., `us-east-1`) for automatic endpoint resolution across all AWS partitions (aws, aws-cn, aws-us-gov). When using a full endpoint URL instead, you must also configure `AWS_REGION` via environment variable or AWS config/profile.
>
> \*\* Azure OpenAI uses `api_key` when set. If `api_key` is omitted, the provider falls back to Microsoft Entra ID via `DefaultAzureCredential` (env vars, workload identity, managed identity, Azure CLI, etc.). The Entra ID path requires build tag: `go build -tags azidentity`.

<details>
<summary><b>Local deployment (Ollama, vLLM, etc.)</b></summary>

**Ollama:**
```json
{
  "model_list": [
    {
      "model_name": "local-llama",
      "model": "ollama/llama3.1:8b",
      "api_base": "http://localhost:11434/v1"
    }
  ]
}
```

**vLLM:**
```json
{
  "model_list": [
    {
      "model_name": "local-vllm",
      "model": "vllm/your-model",
      "api_base": "http://localhost:8000/v1"
    }
  ]
}
```

For full provider configuration details, see [Providers & Models](docs/guides/providers.md).

</details>

## 💬 Channels (Chat Apps)

Talk to your GoGoClaw through 19+ messaging platforms:

| Channel | Setup | Protocol | Docs |
|---------|-------|----------|------|
| **Telegram** | Easy (bot token) | Long polling | [Guide](docs/channels/telegram/README.md) |
| **Discord** | Easy (bot token + intents) | WebSocket | [Guide](docs/channels/discord/README.md) |
| **WhatsApp** | Easy (QR scan or bridge URL) | Native / Bridge | [Guide](docs/guides/chat-apps.md#whatsapp) |
| **Weixin** | Easy (Native QR scan) | iLink API | [Guide](docs/guides/chat-apps.md#weixin) |
| **QQ** | Easy (AppID + AppSecret) | WebSocket | [Guide](docs/channels/qq/README.md) |
| **Slack** | Easy (bot + app token) | Socket Mode | [Guide](docs/channels/slack/README.md) |
| **Matrix** | Medium (homeserver + token) | Sync API | [Guide](docs/channels/matrix/README.md) |
| **Delta Chat** | Easy (account script or email/password) | JSON-RPC (email/E2EE) | [Guide](docs/channels/deltachat/README.md) |
| **DingTalk** | Medium (client credentials) | Stream | [Guide](docs/channels/dingtalk/README.md) |
| **Feishu / Lark** | Medium (App ID + Secret) | WebSocket/SDK | [Guide](docs/channels/feishu/README.md) |
| **LINE** | Medium (credentials + webhook) | Webhook | [Guide](docs/channels/line/README.md) |
| **WeCom** | Easy (QR login or manual) | WebSocket | [Guide](docs/channels/wecom/README.md) |
| **VK** | Easy (group token) | Long Poll | [Guide](docs/channels/vk/README.md) |
| **IRC** | Medium (server + nick) | IRC protocol | [Guide](docs/guides/chat-apps.md#irc) |
| **OneBot** | Medium (WebSocket URL) | OneBot v11 | [Guide](docs/channels/onebot/README.md) |
| **MQTT** | Easy (broker + agent_id) | MQTT pub/sub | [Guide](docs/channels/mqtt/README.md) |
| **MaixCam** | Easy (enable) | TCP socket | [Guide](docs/channels/maixcam/README.md) |
| **Pico** | Easy (enable) | Native protocol | Built-in |
| **Pico Client** | Easy (WebSocket URL) | WebSocket | Built-in |

> All webhook-based channels share a single Gateway HTTP server (`gateway.host`:`gateway.port`, default `127.0.0.1:18790`). Feishu uses WebSocket/SDK mode and does not use the shared HTTP server.

> Log verbosity is controlled by `gateway.log_level` (default: `warn`). Supported values: `debug`, `info`, `warn`, `error`, `fatal`. Can also be set via `GOGOCLAW_LOG_LEVEL`. See [Configuration](docs/guides/configuration.md#gateway-log-level) for details.

For detailed channel setup instructions, see [Chat Apps Configuration](docs/guides/chat-apps.md).

## 🔧 Tools

### 🔍 Web Search

GoGoClaw can search the web to provide up-to-date information. Configure in `tools.web`:

| Search Engine | API Key | Free Tier | Link |
|--------------|---------|-----------|------|
| DuckDuckGo | Not needed | Unlimited | Built-in fallback |
| [Gemini Google Search](https://aistudio.google.com/apikey) | Required | Varies | Gemini with Google Search grounding |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | Required | 1500/month (daily allocation) | AI-powered, China-optimized |
| [Tavily](https://tavily.com) | Required | 1000 queries/month | Optimized for AI Agents |
| [Brave Search](https://brave.com/search/api) | Required | 2000 queries/month | Fast and private |
| [Kagi Search](https://help.kagi.com/kagi/api/search.html) | Required | Paid/limited by API setup | Premium search results |
| [Perplexity](https://www.perplexity.ai) | Required | Paid | AI-powered search |
| [SearXNG](https://github.com/searxng/searxng) | Not needed | Self-hosted | Free metasearch engine |
| [GLM Search](https://open.bigmodel.cn/) | Required | Varies | Zhipu web search |

### ⚙️ Other Tools

GoGoClaw includes built-in tools for file operations, code execution, scheduling, and more. See [Tools Configuration](docs/reference/tools_configuration.md) for details.

## 🎯 Skills

Skills are modular capabilities that extend your Agent. They are loaded from `SKILL.md` files in your workspace.

**Install skills from ClawHub:**

```bash
gogoclaw skills search "web scraping"
gogoclaw skills install <skill-name>
```

**Configure skill registries**:

Add to your `config.json`:
```json
{
  "tools": {
    "skills": {
      "registries": {
        "clawhub": {
          "auth_token": "your-clawhub-token"
        },
        "github": {
          "base_url": "https://github.com",
          "auth_token": "your-github-token",
          "proxy": ""
        }
      }
    }
  }
}
```

`tools.skills.github.*` is deprecated. Use `tools.skills.registries.github.*` instead.

For more details, see [Tools Configuration - Skills](docs/reference/tools_configuration.md#skills-tool).

## 🔗 MCP (Model Context Protocol)

GoGoClaw natively supports [MCP](https://modelcontextprotocol.io/) — connect any MCP server to extend your Agent's capabilities with external tools and data sources.

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "filesystem": {
          "enabled": true,
          "command": "bunx",
          "args": ["--bun", "@modelcontextprotocol/server-filesystem", "/tmp"]
        }
      }
    }
  }
}
```

You can manage common MCP setups directly from the CLI instead of editing JSON by hand:

```bash
gogoclaw mcp add filesystem -- bunx --bun @modelcontextprotocol/server-filesystem /tmp
gogoclaw mcp list
gogoclaw mcp test filesystem
```

`gogoclaw mcp` is a configuration manager: it updates `config.json` under `tools.mcp.servers`, but it does not keep the server process running itself.

Use `gogoclaw mcp edit` when you need advanced fields that are not covered by `gogoclaw mcp add`.
For example, `gogoclaw mcp add` supports `--deferred` and `--env-file`, while `gogoclaw mcp edit` is still useful for direct JSON editing and uncommon MCP settings.

For full MCP configuration (stdio, SSE, HTTP transports, Tool Discovery), see [Tools Configuration - MCP](docs/reference/tools_configuration.md#mcp-tool). For CLI usage and examples, see [MCP Server CLI](docs/reference/mcp-cli.md).

## 🖥️ CLI Reference

| Command                   | Description                      |
| ------------------------- | -------------------------------- |
| `gogoclaw onboard`        | Initialize config & workspace    |
| `gogoclaw auth weixin` | Connect WeChat account via QR |
| `gogoclaw agent -m "..."` | Chat with the agent              |
| `gogoclaw agent`          | Interactive chat mode            |
| `gogoclaw gateway`        | Start the gateway                |
| `gogoclaw status`         | Show status                      |
| `gogoclaw version`        | Show version info                |
| `gogoclaw model`          | View or switch the default model |
| `gogoclaw mcp list`       | List configured MCP servers      |
| `gogoclaw mcp add ...`    | Add or update an MCP server entry |
| `gogoclaw mcp test`       | Probe a configured MCP server    |
| `gogoclaw mcp edit`       | Open config for advanced MCP editing |
| `gogoclaw mcp remove`     | Remove an MCP server entry       |
| `gogoclaw cron list`      | List all scheduled jobs          |
| `gogoclaw cron add ...`   | Add a scheduled job              |
| `gogoclaw cron disable`   | Disable a scheduled job          |
| `gogoclaw cron remove`    | Remove a scheduled job           |
| `gogoclaw skills list`    | List installed skills            |
| `gogoclaw skills install` | Install a skill                  |
| `gogoclaw migrate`        | Migrate data from older versions |
| `gogoclaw auth login`     | Authenticate with providers      |

### ⏰ Scheduled Tasks / Reminders

GoGoClaw supports scheduled reminders and recurring tasks through the `cron` tool:

* **One-time reminders**: "Remind me in 10 minutes" -> triggers once after 10min
* **Recurring tasks**: "Remind me every 2 hours" -> triggers every 2 hours
* **Cron expressions**: "Remind me at 9am daily" -> uses cron expression

See [docs/reference/cron.md](docs/reference/cron.md) for current schedule types, execution modes, command-job gates, and persistence details.

## 📚 Documentation

For detailed guides beyond this README:

| Topic | Description |
|-------|-------------|
| [Docker & Quick Start](docs/guides/docker.md) | Docker Compose setup, Launcher/Agent modes |
| [Chat Apps](docs/guides/chat-apps.md) | All 18+ channel setup guides |
| [Configuration](docs/guides/configuration.md) | Environment variables, workspace layout, security sandbox |
| [MCP Server CLI](docs/reference/mcp-cli.md) | Add, list, test, edit, and remove MCP server entries from the CLI |
| [Scheduled Tasks and Cron Jobs](docs/reference/cron.md) | Cron schedule types, deliver modes, command gates, job storage |
| [Providers & Models](docs/guides/providers.md) | 30+ LLM providers, model routing, model_list configuration |
| [Spawn & Async Tasks](docs/guides/spawn-tasks.md) | Quick tasks, long tasks with spawn, async sub-agent orchestration |
| [Hooks](docs/architecture/hooks/README.md) | Event-driven hook system: observers, interceptors, approval hooks |
| [Steering](docs/architecture/steering.md) | Inject messages into a running agent loop between tool calls |
| [SubTurn](docs/architecture/subturn.md) | Subagent coordination, concurrency control, lifecycle |
| [Troubleshooting](docs/operations/troubleshooting.md) | Common issues and solutions |
| [Tools Configuration](docs/reference/tools_configuration.md) | Per-tool enable/disable, exec policies, MCP, Skills |
| [Hardware Compatibility](docs/guides/hardware-compatibility.md) | Supported container platforms and host requirements |

## 🤝 Contribute & Roadmap

PRs welcome! The codebase is intentionally small and readable.

See [CONTRIBUTING.md](CONTRIBUTING.md) and the repository [issues](https://github.com/biaobiaobiao108/gogoclaw/issues) for guidelines.
