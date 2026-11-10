# Docker 部署

> 返回 [README](../../README.md)

GoGoClaw 生产环境只使用容器镜像。原生二进制和源码构建仅用于开发与测试。本文覆盖 Compose、NAS 直接运行镜像、首次配置、更新备份和实际部署中容易踩到的坑。

## 镜像与端口

发布镜像：

```text
ghcr.io/biaobiaobiao108/gogoclaw:latest
```

支持 Linux `amd64` 和 `arm64`。镜像同时包含：

- `gogoclaw`：CLI 与 gateway
- `gogoclaw-launcher`：Web 控制台，并负责启动和管理 gateway

端口：

| 端口 | 用途 |
|---|---|
| `18800` | Web 控制台 |
| `18790` | gateway 健康检查和频道服务 |

Telegram、微信、Discord、飞书和 QQ 都是主动连接外部平台，常规部署只需发布 `18800`。

## 环境要求

- Docker Engine
- Docker Compose v2，可选
- 64 位 Linux：`amd64` 或 `arm64`
- `/data` 持久化存储
- 访问模型 API 和目标平台的网络；受限网络需要代理
- 暴露到不可信网络时使用 TLS 反向代理

## Docker Compose

```bash
git clone https://github.com/biaobiaobiao108/gogoclaw.git
cd gogoclaw

docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

默认地址为 `http://127.0.0.1:18800`。在 NAS 上需要从其他设备访问时：

```bash
GOGOCLAW_LAUNCHER_BIND=0.0.0.0 \
  docker compose -f docker/docker-compose.yml --profile launcher up -d
```

Compose 使用命名卷 `gogoclaw-data`，推荐作为首次部署方式。

## NAS 直接运行镜像

### 1. 创建数据目录

镜像内的运行用户是 UID/GID `10001`。bind mount 目录也必须归该身份所有：

```bash
sudo mkdir -p /vol1/1000/docker/gogoclaw
sudo chown -R 10001:10001 /vol1/1000/docker/gogoclaw
sudo chmod 750 /vol1/1000/docker/gogoclaw
```

### 2. 启动容器

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

打开 `http://<NAS IP>:18800`。

### 3. 首次配置

1. 创建控制台密码。空 `/data` 不存在默认密码；复用旧 `/data` 时使用旧密码。
2. 添加至少一个模型并设为默认模型。
3. 配置频道。
4. 检查 gateway 和频道状态。
5. 从 Web 或聊天平台发送测试消息。

launcher 会自动管理 gateway。不要同时用同一个 `/data` 启动独立的 `gogoclaw-gateway` 容器。

## 凭据与加密口令

敏感字段最终写入 `/data/.gogoclaw/.security.yml`。如果启用凭据加密，需要提供 `GOGOCLAW_KEY_PASSPHRASE`：

```bash
-e GOGOCLAW_KEY_PASSPHRASE='your-strong-passphrase'
```

口令不要写进镜像、仓库或公开的 Compose 文件。启用后必须妥善备份该口令；丢失后加密凭据无法恢复。

## 模型配置

Web 控制台提供四个通用协议：

| Provider | 协议 |
|---|---|
| `openai` | OpenAI Chat Completions |
| `openai-responses` | OpenAI Responses API |
| `anthropic-messages` | Anthropic Messages API |
| `gemini` | Google Gemini generateContent API |

OpenAI Chat Completions 兼容服务选择 `openai` 并填写自己的 `api_base`。容器访问宿主机上的模型代理时，Linux 通常使用 `http://172.17.0.1:<port>`，不要写 `localhost`，因为容器内的 `localhost` 指容器自身。

模型必须设置成默认模型后，普通频道消息才会进入 Agent。

## 常见部署坑

### 容器启动但无法保存配置

检查宿主机目录所有权：

```bash
sudo chown -R 10001:10001 /vol1/1000/docker/gogoclaw
```

### 局域网无法打开控制台

- 启动参数必须包含 `-public`。
- Docker 必须发布 `18800:18800`。
- 检查 NAS 防火墙和反向代理。
- 如果前面有反向代理，确认它允许 WebSocket 升级，Pico 对话依赖 WebSocket。

### 页面要求密码但从未设置

控制台密码保存在 `/data` 中，不是环境变量。通常是因为挂载了之前使用过的目录或命名卷。保留旧数据时请使用旧密码；需要全新部署时使用空 `/data`。

### Telegram 没有回复

代理必须放在 `settings.proxy`：

```json
{
  "settings": {
    "base_url": "https://api.telegram.org",
    "proxy": "http://user:password@192.168.1.10:7890"
  }
}
```

如果把代理 URL 填进 `base_url`，日志会出现 `decode json: unexpected end of JSON input`，bot 永远不会收到消息。

### 微信绑定成功但没有回复

`allow_from` 必须是 iLink 的真实 `from_user_id`，不能使用微信昵称。新版 Web 扫码绑定会自动写入该 ID；升级前已绑定的账号建议重新扫码一次。

把 `gateway.log_level` 临时设为 `debug`，然后发送一条新消息。拒绝日志示例：

```text
Message rejected by allowlist  from_user_id=o9cq...@im.wechat
```

将精确值写入 `allow_from` 后重启 gateway，完成后恢复 `warn`。

### ARM NAS 拉取缓慢或失败

确认主机架构：

```bash
uname -m
```

`aarch64` 使用 `arm64` 镜像。首次拉取可能需要下载多架构镜像层；如果 GHCR 访问不稳定，配置可靠的容器镜像加速或网络代理。

## 日志与健康检查

```bash
sudo docker logs --tail 200 -f gogoclaw
sudo docker exec gogoclaw tail -n 200 /data/.gogoclaw/logs/gateway.log

curl -fsS http://127.0.0.1:18800/api/auth/status
sudo docker exec gogoclaw curl -fsS http://127.0.0.1:18790/health
sudo docker exec gogoclaw curl -fsS http://127.0.0.1:18790/ready
```

## 更新

```bash
sudo docker pull ghcr.io/biaobiaobiao108/gogoclaw:latest
sudo docker rm -f gogoclaw
# 用与原部署相同的 docker run 参数重新创建，/data 会保留
```

Compose：

```bash
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

## 备份与恢复

备份前先停止所有写入同一数据卷的容器，包括 Launcher 启动的网关、独立网关和一次性 Agent。Compose 部署可执行：

```bash
docker compose -f docker/docker-compose.yml --profile gateway --profile launcher --profile agent stop
```

直接使用 Docker 或 NAS 界面部署的实例也必须停止。只读挂载不能阻止其他容器写入；运行中直接打包 SQLite、WAL 和 JSONL 文件不能保证一致性。以下是停机备份，完成后按原来的部署方式启动服务。在线备份应使用 SQLite Backup API 或一致性快照，不能只复制 `.db` 文件。

命名卷备份：

```bash
docker run --rm \
  -v gogoclaw-data:/data:ro \
  -v "${PWD}:/backup" \
  alpine tar czf /backup/gogoclaw-data.tgz -C /data .
```

宿主机目录备份：

```bash
sudo tar czf gogoclaw-data-$(date +%F).tar.gz \
  -C /vol1/1000/docker/gogoclaw .
```

恢复前确认容器已停止，目标数据目录为空。恢复后把目录所有权重新设为 `10001:10001`。

## 内存与历史保留

Compose 默认容器内存上限为 `2g`，每个 Go 进程的 `GOMEMLIMIT` 为 `512MiB`，Docker 日志按 `10m × 3` 轮转。可通过 `GOGOCLAW_MEMORY_LIMIT` 和 `GOGOCLAW_GO_MEMORY_LIMIT` 调整。Launcher 和它启动的网关共享容器上限，但各自有 Go 内存预算；MCP、模型 CLI、Python、Bun 子进程以及 `/tmp` 还需要额外空间。Go 的软限制不是整个容器的硬限制。较重的工具任务应提高容器上限，依据实际峰值留出余量。

默认 `sessions.db` 保留完整聊天记录，摘要只移动模型上下文的起点，网页历史和搜索仍可访问原消息。删除会话会把页归还给 SQLite freelist 供后续写入复用，但数据库文件不会立即缩小；GoGoClaw 刻意避免反复执行 `VACUUM`。备份时必须包含 SQLite 的 WAL 文件集合；不再需要的历史可通过删除会话清理。

长期记忆注入默认限制为约 `2048` 个估算 token，且最多使用当前模型可用上下文的八分之一。可用 `agents.defaults.memory_context_tokens` 或 `GOGOCLAW_AGENTS_DEFAULTS_MEMORY_CONTEXT_TOKENS` 配置；`MEMORY.md` 和最近三天日记的原文件不受截取影响，Agent 可用文件工具读取完整内容。Seahorse 在首次使用会话时核对历史，不再启动时全量加载。

## 安全边界

Compose 和本文的直接运行命令都使用：

- UID/GID `10001`
- 只读根文件系统
- 删除全部 Linux capabilities
- `no-new-privileges`
- 有大小限制的可写 `/tmp`

容器是执行沙箱，因此 `tools.approval` 默认关闭。不要挂载 Docker socket，也不要挂载无关的宿主机目录。
