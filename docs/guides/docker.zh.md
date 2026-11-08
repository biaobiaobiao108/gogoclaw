# Docker 部署

> 返回 [README](../../README.md)

PicoClaw 生产环境仅使用容器镜像。原生二进制和源码构建只用于开发与测试。

## 环境要求

- Docker Engine 和 Compose v2
- 为 `/data` 使用持久化卷
- Web 控制台或 Webhook 暴露到本机以外前，在反向代理中终止 TLS

发布镜像同时包含 `picoclaw` 和 `picoclaw-launcher`。Compose 使用命名卷 `picoclaw-data` 保存状态。

## Web 控制台

```bash
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

打开 `http://127.0.0.1:18800`，创建控制台密码，然后配置模型、Channel 和工具。launcher 会生成初始配置，并在同一容器中管理 gateway 进程。

宿主机端口默认只绑定回环地址。需要远程访问时，使用经过加固的 TLS 反向代理：

```bash
PICOCLAW_LAUNCHER_BIND=0.0.0.0 \
  docker compose -f docker/docker-compose.yml --profile launcher up -d
```

只要 `config.json` 或 Web 控制台经过不可信网络，就必须使用 HTTPS。

## 无头 Gateway

首先生成初始配置：

```bash
docker compose -f docker/docker-compose.yml --profile gateway pull
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint picoclaw picoclaw-gateway onboard
```

然后启动 gateway：

```bash
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

需要接收 Webhook 的 Channel 使用端口 `18790`。宿主机默认只绑定 `127.0.0.1`；只有反向代理或可信内网需要访问时才设置 `PICOCLAW_GATEWAY_BIND`。

## 一次性命令

```bash
docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint picoclaw picoclaw-gateway agent -m "2+2 等于多少？"

docker compose -f docker/docker-compose.yml --profile gateway run --rm \
  --entrypoint picoclaw picoclaw-gateway status
```

## 数据与更新

数据卷挂载到 `/data`，其中包含 `/data/.picoclaw/config.json`、凭据、工作区、会话和日志。

```bash
# 备份
docker run --rm \
  -v picoclaw-data:/data:ro \
  -v "${PWD}:/backup" \
  alpine tar czf /backup/picoclaw-data.tgz -C /data .

# 恢复到空数据卷
docker run --rm \
  -v picoclaw-data:/data \
  -v "${PWD}:/backup" \
  alpine tar xzf /backup/picoclaw-data.tgz -C /data

# 更新
docker compose -f docker/docker-compose.yml --profile launcher pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

项目不再提供应用内自更新命令。

## 安全边界

Compose 服务使用 UID/GID `10001`、只读根文件系统、删除全部 capabilities、`no-new-privileges`，并为 `/tmp` 提供有大小限制的可写文件系统。

工作区路径检查用于减少误写，不是操作系统级沙箱。生产安全边界是容器；不要挂载 Docker socket 或无关的宿主机目录。

## 开发

本地构建和直接运行进程仅用于开发：

```bash
make build
make build-launcher
cd web && make dev
```

本地容器开发使用开发覆盖文件：

```bash
docker compose -f docker/docker-compose.yml \
  -f docker/docker-compose.dev.yml --profile gateway up --build
```
