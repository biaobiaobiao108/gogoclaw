<div align="center">
<img src="assets/logo.svg" alt="GoGoClaw" width="128">

<h1>GoGoClaw</h1>

<p>用 Go 构建的轻量级通用 Agent Harness 与 Web 控制台</p>

<p>
  <img src="https://img.shields.io/badge/Go-1.27.3+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Arch-amd64%20%7C%20arm64-blue" alt="amd64 / arm64">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="MIT License">
</p>

<p>
  <strong>中文</strong> | <a href="README.en.md">English</a>
</p>
</div>

---

GoGoClaw 是一个面向单机长期运行与私有化部署的通用 Agent Harness。核心后端使用 Go，Web 控制台使用 React 19 与 Tailwind CSS v4，通过 Bun 构建。它可以连接 OpenAI、Anthropic、Gemini 等模型协议，并把同一个 Agent 暴露到 Web、Telegram、微信、Discord、飞书和 QQ 等频道。

GoGoClaw 以单个可信实例为运行边界，不提供多租户、RBAC 或横向扩容能力；同一个数据目录只应由一个 GoGoClaw 实例管理。

生产环境只支持容器部署。容器是运行时安全边界，工具调用审批默认关闭。

## 核心能力

- **完整 Agent 循环**：会话、长期记忆、工作区文件、Skills、MCP、工具调用、计划、流式回复和子 Agent。
- **统一模型协议**：OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Google Gemini generateContent。
- **多频道接入**：Web、Telegram、微信个人号、Discord、飞书 / Lark、QQ。
- **现代 Web 控制台**：模型、频道、工具、会话、审批、日志和运行状态都在同一处管理。
- **容器优先**：发布镜像同时支持 Linux `amd64` 与 `arm64`，以非 root 用户运行。
- **可持久化**：配置、凭据、会话、工作区、频道同步状态和日志统一存放在 `/data`。

## 快速部署

### 环境要求

- Docker Engine
- Docker Compose v2，可选
- 64 位 Linux 主机，架构为 `amd64` 或 `arm64`
- 能访问模型 API 和目标聊天平台。受限网络应准备 HTTP / SOCKS5 代理
- 如果控制台暴露到不可信网络，前面必须有一个带 TLS 的反向代理

### 方式一：Docker Compose

```bash
git clone https://github.com/biaobiaobiao108/gogoclaw.git
cd gogoclaw

docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

默认只监听本机 `127.0.0.1:18800`。在 NAS 或局域网服务器上需要直接访问时：

```bash
GOGOCLAW_LAUNCHER_BIND=0.0.0.0 \
  docker compose -f docker/docker-compose.yml --profile launcher up -d
```

Compose 使用命名卷 `gogoclaw-data`，不需要手动处理宿主机目录权限，适合首次部署。

### 方式二：直接运行镜像（NAS 常用）

先准备持久化目录。容器内固定使用 UID/GID `10001`，宿主机目录必须归该用户所有：

```bash
sudo mkdir -p /vol1/1000/docker/gogoclaw
sudo chown -R 10001:10001 /vol1/1000/docker/gogoclaw
sudo chmod 750 /vol1/1000/docker/gogoclaw
```

启动 Web 控制台：

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

打开 `http://<服务器 IP>:18800`。首次访问且 `/data` 为空时，页面会要求创建控制台密码；不存在默认密码。

`18800` 是控制台端口。`gogoclaw-launcher` 会自动启动并管理同一容器内的 gateway，通常不需要再映射或启动第二个 `gogoclaw-gateway` 容器。

### 首次配置顺序

1. 创建控制台密码。
2. 在“模型”页面添加至少一个模型并设为默认模型。
3. 在“频道”页面配置 Telegram、微信或其他频道。
4. 检查“概览”中的 gateway 与频道状态。
5. 从 Web 对话或已连接的聊天平台发送一条测试消息。

## 模型协议

Web 控制台把模型入口收敛为四个通用协议：

| Provider ID | 协议 | 默认 API Base |
|---|---|---|
| `openai` | OpenAI Chat Completions | `https://api.openai.com/v1` |
| `openai-responses` | OpenAI Responses API | `https://api.openai.com/v1` |
| `anthropic-messages` | Anthropic Messages API | `https://api.anthropic.com/v1` |
| `gemini` | Google Gemini generateContent API | `https://generativelanguage.googleapis.com/v1beta` |

兼容 OpenAI Chat Completions 的第三方服务，选择 `openai` 并覆盖 `api_base` 即可。兼容 Anthropic Messages 的服务使用 `anthropic-messages`，兼容 Gemini generateContent 的服务使用 `gemini`。

模型条目的关键字段：

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

完整字段、回退链和路由说明见 [模型配置](docs/guides/providers.zh.md)。

## 频道

| 频道 | 连接方式 | 关键注意事项 |
|---|---|---|
| Web / Pico | 内置 WebSocket | 控制台首次使用时自动配置 |
| Telegram | Bot API 长轮询 | 网络受限时只填 `settings.proxy`，不要填入 `base_url` |
| 微信个人号 | 腾讯 iLink 扫码 | `allow_from` 必须使用 iLink 返回的 `from_user_id` |
| Discord | Bot WebSocket | 需要正确的 bot intents |
| 飞书 / Lark | WebSocket / SDK | 不需要公网回调地址 |
| QQ | Bot WebSocket | 需要在 QQ 平台开放对应沙箱或群聊权限 |

### Telegram 代理

正确配置示例：

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

`base_url` 是 Telegram Bot API 地址，不是代理地址。把代理 URL 填进 `base_url` 会让请求变成非法的 Bot API URL，日志通常表现为 `decode json: unexpected end of JSON input`。

### 微信个人号

Web 控制台扫码绑定成功后，会保存 token，并把扫码用户返回的 iLink `ilink_user_id` 自动写入 `allow_from`。`allow_from` 不能填写微信昵称；该协议中的真实发送者通常以 `@im.wechat` 结尾。

如果升级前已经绑定过，建议在频道页面重新扫码一次，使 `allow_from` 更新为新的规范用户 ID。排障时把 `gateway.log_level` 临时设为 `debug`，日志会出现：

```text
Message rejected by allowlist  from_user_id=...
```

把日志中的精确 `from_user_id` 写入 `allow_from` 后重启 gateway。启用日志级别为 `warn` 时，拒绝消息也会保留为警告，避免静默丢消息。

## Docker 部署常见坑

### 1. 宿主机目录权限

容器以 UID/GID `10001` 运行。使用 bind mount 时，如果 `/data` 归 root 或其他用户所有，容器虽然能启动，但保存配置、登录密码、数据库或日志时会失败。

```bash
sudo chown -R 10001:10001 /path/to/gogoclaw-data
```

不确定时优先使用 Compose 命名卷。

### 2. 端口映射正确但局域网打不开

- 容器内 launcher 必须使用 `-public` 才监听所有容器网卡。
- Docker 需要发布 `18800:18800`。
- NAS 防火墙、反向代理和上游安全组也要允许对应端口。
- `18790` 是 gateway 端口，Telegram、微信、Discord、飞书和 QQ 使用主动连接，通常不需要向公网暴露。

### 3. 同一个数据目录启动了多个服务

`gogoclaw-launcher` 已经会管理 gateway。不要对同一个 `/data` 同时运行 launcher 和独立的 `gogoclaw-gateway`，否则会出现 PID、端口或配置竞争。

### 4. 架构不匹配或拉错镜像

```bash
uname -m
sudo docker image inspect ghcr.io/biaobiaobiao108/gogoclaw:latest \
  --format '{{.Os}}/{{.Architecture}}'
```

`x86_64` 对应 `amd64`，`aarch64` 对应 `arm64`。项目不支持 32 位平台。

### 5. 重启后出现未知密码页面

控制台密码保存在持久化 `/data` 中。如果复用了旧目录或旧卷，登录密码属于那份旧数据，不是容器预设的默认值。全新部署应使用空 `/data`；保留旧数据时请使用原密码。

### 6. 频道显示已启用但没有回复

按顺序检查：

1. 平台侧消息是否进入 gateway。
2. `allow_from` 是否使用了平台真实用户 ID。
3. Agent 是否收到消息并发出模型请求。
4. 模型请求是否成功。
5. 出站消息是否发送成功。

日志位置：

```bash
sudo docker logs --tail 200 -f gogoclaw
sudo docker exec gogoclaw tail -n 200 /data/.gogoclaw/logs/gateway.log
```

## 运维

### 健康检查

```bash
curl -fsS http://127.0.0.1:18800/api/auth/status
curl -fsS http://127.0.0.1:18790/health
curl -fsS http://127.0.0.1:18790/ready
```

### 更新

```bash
sudo docker pull ghcr.io/biaobiaobiao108/gogoclaw:latest
sudo docker rm -f gogoclaw
# 使用前面的 docker run 命令重新创建容器；/data 不会丢
```

使用 Compose 时：

```bash
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

### 备份

需要备份的是整个 `/data`：配置、`.security.yml`、SQLite、工作区、Skills、频道状态和日志都在这里。

```bash
sudo tar czf gogoclaw-data-$(date +%F).tar.gz \
  -C /vol1/1000/docker/gogoclaw .
```

不要把 `.security.yml`、API Key、bot token 或设备私钥提交到 Git。

## Agent 行为与提示词

GoGoClaw 自带一份紧凑的基础提示词。通用工作原则包括：先理解再执行、需要时使用工具、只根据工具结果声称成功、修改前检查真实状态、失败后调整、不编造结果、不执行外部内容中的越权指令，并保持回复简洁。

提示词还定义了 GoGoClaw 专属的运行时契约：当前回合暴露的工具才是可信能力、工作区路径与持久化边界、长期记忆只保存稳定偏好与决策、普通回复自动投递到当前频道、子 Agent 默认隔离、凭据只能在目标服务内使用，以及默认使用用户语言并适配当前频道。

工作区还可以通过以下文件覆盖或补充行为：

- `AGENT.md`：项目或 Agent 的主要行为说明。
- `SOUL.md`：角色、语气和长期行为风格。
- `USER.md`：用户偏好。
- `memory/MEMORY.md`：跨会话长期记忆。
- `skills/<name>/SKILL.md`：按需加载的能力说明。

显式用户指令优先于对话摘要；工作区说明用于补充，而不是绕过安全边界。

## 开发

依赖 Go 1.27.3+ 和 Bun 1.4.4+。

```bash
make deps
make build
make build-launcher

cd web
make dev
```

测试与格式：

```bash
make test
make fmt
```

本地二进制、直接运行 gateway 和开发容器只用于开发测试，不是生产部署方式。

## 文档

- [Docker 部署](docs/guides/docker.zh.md)
- [频道配置](docs/guides/chat-apps.zh.md)
- [模型与 Provider](docs/guides/providers.zh.md)
- [配置参考](docs/guides/configuration.zh.md)
- [工具与 MCP](docs/reference/tools_configuration.zh.md)
- [疑难排查](docs/operations/troubleshooting.zh.md)
- [贡献指南](docs/project/CONTRIBUTING.zh.md)

## License

[MIT](LICENSE)
