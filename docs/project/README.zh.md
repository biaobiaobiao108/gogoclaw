<div align="center">
<img src="../../assets/logo.webp" alt="PicoClaw" width="512">

<h1>PicoClaw: 基于Go语言的超高效 AI 助手</h1>

<h3>Linux 容器 · amd64 / arm64 · 皮皮虾，我们走！</h3>
  <p>
    <img src="https://img.shields.io/badge/Go-1.27.3+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Arch-x86__64%2C%20ARM64%2C%20RISC--V-blue" alt="Hardware">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
    <br>
    <a href="https://picoclaw.io"><img src="https://img.shields.io/badge/Website-picoclaw.io-blue?style=flat&logo=google-chrome&logoColor=white" alt="Website"></a>
    <a href="https://docs.picoclaw.io/"><img src="https://img.shields.io/badge/Docs-Official-007acc?style=flat&logo=read-the-docs&logoColor=white" alt="Docs"></a>
    <a href="https://deepwiki.com/sipeed/picoclaw"><img src="https://img.shields.io/badge/Wiki-DeepWiki-FFA500?style=flat&logo=wikipedia&logoColor=white" alt="Wiki"></a>
    <br>
    <a href="https://x.com/SipeedIO"><img src="https://img.shields.io/badge/X_(Twitter)-SipeedIO-black?style=flat&logo=x&logoColor=white" alt="Twitter"></a>
    <a href="../../assets/wechat.png"><img src="https://img.shields.io/badge/WeChat-Group-41d56b?style=flat&logo=wechat&logoColor=white"></a>
    <a href="https://discord.gg/V4sAZ9XWpN"><img src="https://img.shields.io/badge/Discord-Community-4c60eb?style=flat&logo=discord&logoColor=white" alt="Discord"></a>
  </p>

**中文** | [English](../../README.md)

</div>

---

> **PicoClaw** 是由 [矽速科技 (Sipeed)](https://sipeed.com) 发起的独立开源项目，完全使用 **Go 语言**从零编写——不是 OpenClaw、NanoBot 或其他项目的分支。

🦐 **PicoClaw** 是一个受 [NanoBot](https://github.com/HKUDS/nanobot) 启发的超轻量级个人 AI 助手。它采用 **Go 语言** 从零重构，经历了一个"自举"过程——即由 AI Agent 自身驱动了整个架构迁移和代码优化。

⚡️ **容器优先**：生产镜像支持 64 位 x86 和 ARM Linux。原生二进制只用于开发与测试。

> [!CAUTION]
> **🚨 安全声明**
>
> - **无加密货币 (NO CRYPTO):** PicoClaw **没有** 发行任何官方代币、Token 或虚拟货币。所有在 `pump.fun` 或其他交易平台上的相关声称均为 **诈骗**。
> - **官方域名:** 唯一的官方网站是 **[picoclaw.io](https://picoclaw.io)**，公司官网是 **[sipeed.com](https://sipeed.com)**。
> - **警惕:** 许多 `.ai/.org/.com/.net/...` 后缀的域名被第三方抢注，请勿轻信。
> - **注意:** PicoClaw 正在初期的快速功能开发阶段。生产部署仅支持容器镜像，暴露到公网前请先阅读安全说明。
> - **注意:** PicoClaw 最近合并了大量 PR，近期版本可能内存占用较大 (10~20MB)，我们将在功能较为收敛后进行资源占用优化。

## 📢 新闻

2026-03-25 🚀 **v0.2.4 发布！** Agent 架构全面重构（SubTurn、Hook、Steering、EventBus）、微信/企业微信深度集成、安全体系升级（.security.yml、敏感数据过滤）、新增 Provider（AWS Bedrock、Azure、小米 MiMo），以及 35 项 Bug 修复。PicoClaw 已达 **26K ⭐**！

2026-03-09 🎉 **v0.2.1 — 史上最大更新！** MCP 协议支持、4 个新频道 (Matrix/IRC/WeCom/Discord Proxy)、3 个新 Provider (Kimi/Minimax/Avian)、视觉管线、JSONL 记忆存储、模型路由。

2026-02-28 📦 **v0.2.0** 发布，支持 Docker Compose 和 Web UI 启动器。

<details>
<summary>更早的新闻...</summary>

2026-02-26 🎉 PicoClaw 仅 17 天突破 **20K Stars**！频道自动编排和能力接口上线。

2026-02-16 🎉 PicoClaw 一周内突破 12K Stars！社区维护者角色和 [路线图](../../ROADMAP.md) 正式发布。

2026-02-13 🎉 PicoClaw 4 天内突破 5000 Stars！项目路线图和开发者群组筹建中。

</details>

## ✨ 特性

🪶 **精简 Go 核心**: Agent 使用 Go 实现，自身不依赖 Node.js 或 Python 应用运行时；生产容量由容器宿主机提供。


🌍 **容器优先**: 生产镜像支持 x86-64 和 ARM64 Linux。

🤖 **AI 自举**: 纯 Go 语言原生实现 — 95% 的核心代码由 Agent 生成，并经由"人机回环"微调。

🔌 **MCP 支持**: 原生 [Model Context Protocol](https://modelcontextprotocol.io/) 集成 — 连接任意 MCP 服务器扩展 Agent 能力。

👁️ **视觉管线**: 直接向 Agent 发送图片和文件 — 自动 base64 编码对接多模态 LLM。

🧠 **智能路由**: 基于规则的模型路由 — 简单查询走轻量模型，节省 API 成本。

> 📋 **[硬件兼容列表](../guides/hardware-compatibility.zh.md)** — 查看可运行容器镜像的 Linux 主机。你的板卡没在列表中？欢迎提交 PR！

## 🦾 演示

### 🛠️ 标准助手工作流

<table align="center">
<tr align="center">
<th><p align="center">🧩 全栈工程师模式</p></th>
<th><p align="center">🗂️ 日志与规划管理</p></th>
<th><p align="center">🔎 网络搜索与学习</p></th>
</tr>
<tr>
<td align="center"><p align="center"><img src="../../assets/picoclaw_code.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/picoclaw_memory.gif" width="240" height="180"></p></td>
<td align="center"><p align="center"><img src="../../assets/picoclaw_search.gif" width="240" height="180"></p></td>
</tr>
<tr>
<td align="center">开发 • 部署 • 扩展</td>
<td align="center">日程 • 自动化 • 记忆</td>
<td align="center">发现 • 洞察 • 趋势</td>
</tr>
</table>

### 🐜 容器宿主机

生产环境运行在支持 `amd64` 或 `arm64` 容器的 64 位 Linux 主机上。宿主机要求参见 [硬件兼容性列表](../guides/hardware-compatibility.zh.md)。

## 📦 生产部署（仅容器）

生产环境仅支持使用发布的 Linux 容器镜像。源码构建的原生二进制只用于开发和测试。

### 环境要求

- Docker Engine 和 Docker Compose v2
- 为 `/data` 使用持久化 Docker 卷
- 如果 Web 控制台或 Webhook 需要暴露到本机以外，使用带 TLS 的反向代理
- 能够访问模型 Provider 和聊天平台的外网

### 使用 Web 控制台启动

```bash
git clone https://github.com/sipeed/picoclaw.git
cd picoclaw

docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

打开 `http://127.0.0.1:18800`，创建控制台密码，然后配置模型、Channel 和工具。首次使用时 launcher 会生成初始配置，并在同一容器内管理 gateway 进程。

launcher 端口默认只绑定回环地址。不要直接暴露到公网；如需远程访问，应放在经过加固的 TLS 反向代理之后：

```bash
# 仅在 TLS 反向代理或可信内网后方使用。
PICOCLAW_LAUNCHER_BIND=0.0.0.0 \
  docker compose -f docker/docker-compose.yml --profile launcher up -d
```

### 无头 Gateway

先交互式生成初始配置：

```bash
docker compose -f docker/docker-compose.yml --profile gateway pull
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint picoclaw picoclaw-gateway onboard
```

再启动 gateway：

```bash
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

需要接收 Webhook 的 Channel 使用 gateway 端口 `18790`。宿主机默认只绑定 `127.0.0.1`；只有反向代理或可信网络需要访问时才修改 `PICOCLAW_GATEWAY_BIND`。

### 一次性 CLI 命令

```bash
# 单次提问
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint picoclaw picoclaw-gateway agent -m "2+2 等于多少？"

# 查看状态
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint picoclaw picoclaw-gateway status
```

### 数据、备份与更新

应用状态保存在命名卷 `picoclaw-data` 中，并挂载到容器内 `/data`。其中包括 `/data/.picoclaw/config.json`、凭据、工作区、会话和日志。

```bash
# 备份数据卷
docker run --rm \
  -v picoclaw-data:/data:ro \
  -v "${PWD}:/backup" \
  alpine tar czf /backup/picoclaw-data.tgz -C /data .

# 恢复到空数据卷（卷中不能已有状态）
docker run --rm \
  -v picoclaw-data:/data \
  -v "${PWD}:/backup" \
  alpine tar xzf /backup/picoclaw-data.tgz -C /data
```

更新时拉取新镜像并重建服务：

```bash
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

项目不再提供应用内自更新。

### 容器安全边界

生产镜像使用 UID/GID `10001` 运行，以 `tini` 作为 PID 1；配套 Compose 配置启用：

- 只读根文件系统
- 删除全部 Linux capabilities
- `no-new-privileges`
- 有大小限制的可写 `/tmp`
- 默认仅绑定回环地址

工作区路径检查用于防止误写，不是操作系统级沙箱。生产安全边界是容器本身；不要把 Docker socket 或不必要的宿主机目录挂载进去。

### 开发构建

本地构建仅用于开发和测试。依赖：

- Go 1.27.3+
- Bun 1.4.4+
- Windows 上需要 PowerShell 7

```bash
make deps
make build
make build-launcher

# 完整 launcher 开发流程
cd web
make dev
```

本地自动打开浏览器、`-public` 和直接运行 gateway 都是开发便利功能，不属于受支持的生产部署方式。

需要在本机测试容器构建时，使用开发覆盖文件：

```bash
docker compose -f docker/docker-compose.yml \
  -f docker/docker-compose.dev.yml --profile launcher up --build
```

## 🔌 Providers (LLM)

PicoClaw 通过 `model_list` 配置支持 30+ LLM Provider，使用 `协议/模型` 格式：

| Provider | 协议 | API Key | 备注 |
|----------|------|---------|------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | 必填 | GPT-5.4、GPT-4o、o3 等 |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | 必填 | Claude Opus 4.6、Sonnet 4.6 等 |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | 必填 | Gemini 3 Flash、2.5 Pro 等 |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | 必填 | 200+ 模型，统一 API |
| [智谱 (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | 必填 | GLM-4.7、GLM-5 等 |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | 必填 | DeepSeek-V3、DeepSeek-R1 |
| [火山引擎](https://console.volcengine.com) | `volcengine/` | 必填 | 豆包、Ark 系列模型 |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | 必填 | Qwen3、Qwen-Max 等 |
| [Groq](https://console.groq.com/keys) | `groq/` | 必填 | 快速推理（Llama、Mixtral） |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | 必填 | Kimi 系列模型 |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | 必填 | MiniMax 系列模型 |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | 必填 | Mistral Large、Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | 必填 | NVIDIA 托管模型 |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | 必填 | 快速推理 |
| [Novita AI](https://novita.ai/) | `novita/` | 必填 | 多种开源模型 |
| [小米 MiMo](https://platform.xiaomimimo.com/) | `mimo/` | 必填 | MiMo 系列模型 |
| [Ollama](https://ollama.com/) | `ollama/` | 无需 | 本地模型，自托管 |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | 无需 | 本地部署，兼容 OpenAI |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | 视情况 | 100+ Provider 代理 |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | 必填 | 企业级 Azure 部署 |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | 设备码登录 |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |

<details>
<summary><b>本地部署（Ollama、vLLM 等）</b></summary>

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

完整 Provider 配置详情请参阅 [Providers & Models](../guides/providers.zh.md)。

</details>

## 💬 Channels（聊天应用）

通过 18+ 消息平台与你的 PicoClaw 对话：

| Channel | 配置难度 | 协议 | 文档 |
|---------|----------|------|------|
| **Telegram** | 简单（bot token） | 长轮询 | [指南](../channels/telegram/README.zh.md) |
| **Discord** | 简单（bot token + intents） | WebSocket | [指南](../channels/discord/README.zh.md) |
| **WhatsApp** | 简单（扫码或 bridge URL） | 原生 / Bridge | [指南](../guides/chat-apps.zh.md#whatsapp) |
| **微信 (Weixin)** | 简单（扫码登录） | iLink API | [指南](../guides/chat-apps.zh.md#weixin) |
| **QQ** | 简单（AppID + AppSecret） | WebSocket | [指南](../channels/qq/README.zh.md) |
| **Slack** | 简单（bot + app token） | Socket Mode | [指南](../channels/slack/README.zh.md) |
| **Matrix** | 中等（homeserver + token） | Sync API | [指南](../channels/matrix/README.zh.md) |
| **钉钉** | 中等（client credentials） | Stream | [指南](../channels/dingtalk/README.zh.md) |
| **飞书 / Lark** | 中等（App ID + Secret） | WebSocket/SDK | [指南](../channels/feishu/README.zh.md) |
| **LINE** | 中等（credentials + webhook） | Webhook | [指南](../channels/line/README.zh.md) |
| **企业微信** | 简单（扫码登录或手动配置） | WebSocket | [指南](../channels/wecom/README.zh.md) |
| **VK** | 简单（群组 token） | Long Poll | [指南](../channels/vk/README.md) |
| **IRC** | 中等（server + nick） | IRC 协议 | [指南](../guides/chat-apps.zh.md#irc) |
| **OneBot** | 中等（WebSocket URL） | OneBot v11 | [指南](../channels/onebot/README.zh.md) |
| **MaixCam** | 简单（启用即可） | TCP socket | [指南](../channels/maixcam/README.zh.md) |
| **Pico** | 简单（启用即可） | 原生协议 | 内置 |
| **Pico Client** | 简单（WebSocket URL） | WebSocket | 内置 |

> 所有基于 Webhook 的 Channel 共用同一个 Gateway HTTP 服务器（`gateway.host`:`gateway.port`，默认 `127.0.0.1:18790`）。飞书使用 WebSocket/SDK 模式，不使用共享 HTTP 服务器。

> 日志详细程度通过 `gateway.log_level` 控制（默认：`warn`）。支持的值：`debug`、`info`、`warn`、`error`、`fatal`。也可通过 `PICOCLAW_LOG_LEVEL` 环境变量设置。详见[配置指南](../guides/configuration.zh.md#gateway-日志等级)。

详细 Channel 配置说明请参阅 [聊天应用配置](../guides/chat-apps.zh.md)。

## 🔧 Tools

### 🔍 网络搜索

PicoClaw 可以搜索网络以提供最新信息。在 `tools.web` 中配置：

| 搜索引擎 | API Key | 免费额度 | 链接 |
|---------|---------|---------|------|
| [百度搜索](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | 必填 | 1500 次/月（按天发放） | AI 搜索，国内首选 |
| [Tavily](https://tavily.com) | 必填 | 1000 次/月 | 专为 AI Agent 优化 |
| [GLM Search](https://open.bigmodel.cn/) | 必填 | 视情况 | 智谱网络搜索 |
| DuckDuckGo | 无需 | 无限制 | 内置备用（国内访问困难） |
| [Perplexity](https://www.perplexity.ai) | 必填 | 付费 | AI 驱动搜索（国内访问困难） |
| [Brave Search](https://brave.com/search/api) | 必填 | 2000 次/月 | 快速且注重隐私（国内访问困难） |
| [SearXNG](https://github.com/searxng/searxng) | 无需 | 自托管 | 免费元搜索引擎 |

### ⚙️ 其他工具

PicoClaw 内置文件操作、代码执行、定时任务等工具。详情请参阅 [工具配置](../reference/tools_configuration.zh.md)。

## 🎯 Skills

Skills 是扩展 Agent 能力的模块化插件，从工作区的 `SKILL.md` 文件加载。

**从 ClawHub 安装 Skills：**

```bash
picoclaw skills search "web scraping"
picoclaw skills install <skill-name>
```

**配置 Skills 仓库源**：

在 `config.json` 中添加：
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

`tools.skills.github.*` 已废弃，请改用 `tools.skills.registries.github.*`。

更多详情请参阅 [工具配置 - Skills](../reference/tools_configuration.zh.md#skills-tool)。

## 🔗 MCP (Model Context Protocol)

PicoClaw 原生支持 [MCP](https://modelcontextprotocol.io/) — 连接任意 MCP 服务器，通过外部工具和数据源扩展 Agent 能力。

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

完整 MCP 配置（stdio、SSE、HTTP 传输、Tool Discovery）请参阅 [工具配置 - MCP](../reference/tools_configuration.zh.md#mcp-tool)。

## <img src="../../assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> 加入 Agent 社交网络

通过 CLI 或任何已集成的聊天应用发送一条消息，即可将 PicoClaw 连接到 Agent 社交网络。

**阅读 `https://clawdchat.ai/skill.md` 并按照说明加入 [ClawdChat.ai](https://clawdchat.ai)**

## 🖥️ CLI 命令行参考

| 命令                       | 说明                   |
| ------------------------- | ---------------------- |
| `picoclaw onboard`        | 初始化配置与工作区       |
| `picoclaw auth weixin` | 扫码连接微信个人号 |
| `picoclaw agent -m "..."` | 与 Agent 对话           |
| `picoclaw agent`          | 交互式对话模式           |
| `picoclaw gateway`        | 启动网关                |
| `picoclaw status`         | 查看状态                |
| `picoclaw version`        | 查看版本信息             |
| `picoclaw model`          | 查看或切换默认模型       |
| `picoclaw cron list`      | 列出所有定时任务         |
| `picoclaw cron add ...`   | 添加定时任务             |
| `picoclaw cron disable`   | 禁用定时任务             |
| `picoclaw cron remove`    | 删除定时任务             |
| `picoclaw skills list`    | 列出已安装 Skills        |
| `picoclaw skills install` | 安装 Skill              |
| `picoclaw migrate`        | 从旧版本迁移数据         |
| `picoclaw auth login`     | 认证 Provider           |

### ⏰ 定时任务 / 提醒

PicoClaw 通过 `cron` 工具支持定时提醒和重复任务：

* **一次性提醒**: "10分钟后提醒我" → 10分钟后触发一次
* **重复任务**: "每2小时提醒我" → 每2小时触发
* **Cron 表达式**: "每天上午9点提醒我" → 使用 cron 表达式

## 📚 文档

详细指南请参阅以下文档，README 仅涵盖快速入门。

| 主题 | 说明 |
|------|------|
| 🐳 [Docker 与快速开始](../guides/docker.zh.md) | Docker Compose 配置、Launcher/Agent 模式、快速开始 |
| 💬 [聊天应用配置](../guides/chat-apps.zh.md) | 全部 17+ Channel 配置指南 |
| ⚙️ [配置指南](../guides/configuration.zh.md) | 环境变量、工作区布局、安全沙箱 |
| 🔌 [提供商与模型配置](../guides/providers.zh.md) | 30+ LLM Provider、模型路由、model_list 配置 |
| 🔄 [异步任务与 Spawn](../guides/spawn-tasks.zh.md) | 快速任务、长任务与 Spawn、异步子 Agent 编排 |
| 🪝 [Hook 系统](../architecture/hooks/README.zh.md) | 事件驱动 Hook：观察者、拦截器、审批 Hook |
| 🎯 [Steering](../architecture/steering.md) | 在工具调用间向运行中的 Agent 注入消息 |
| 🔀 [SubTurn](../architecture/subturn.md) | 子 Agent 协调、并发控制、生命周期管理 |
| 🐛 [疑难解答](../operations/troubleshooting.zh.md) | 常见问题与解决方案 |
| 🔧 [工具配置](../reference/tools_configuration.zh.md) | 工具启用/禁用、执行策略、MCP、Skills |
| 📋 [硬件兼容列表](../guides/hardware-compatibility.zh.md) | 支持的容器平台与宿主机要求 |

## 🤝 贡献与路线图

欢迎提交 PR！代码库刻意保持小巧和可读。🤗

查看完整的 [社区路线图](https://github.com/sipeed/picoclaw/issues/988) 和 [CONTRIBUTING.md](../../CONTRIBUTING.md)。

开发者群组正在组建中，入群门槛：至少合并过 1 个 PR。

用户群组：

Discord: <https://discord.gg/V4sAZ9XWpN>

WeChat:
<img src="../../assets/wechat.png" alt="WeChat group QR code" width="512">
