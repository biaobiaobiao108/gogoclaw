# GoGoClaw 容器宿主机兼容性

生产环境 GoGoClaw 仅使用发布的 Linux 容器镜像运行。原生二进制和源码构建只用于开发与测试。

## 支持的镜像平台

| 平台 | 状态 | 宿主机要求 |
|------|------|------------|
| `linux/amd64` | 支持 | 64 位 x86 Linux，具备 Docker 或其他 OCI 运行时 |
| `linux/arm64` | 支持 | 64 位 ARM Linux，具备 Docker 或其他 OCI 运行时 |
项目仅发布 `amd64` 和 `arm64` 镜像。Windows 和 macOS 仅作为开发环境；生产容器必须运行在 Linux 上。

## 宿主机要求

- Docker Engine（启用 BuildKit），或其他兼容的 OCI 运行时
- 使用仓库提供的 `docker/docker-compose.yml` 时需要 Docker Compose v2
- 容器至少可用的 512MB 内存，建议 1GB 以上
- 至少 1GB 可用存储，用于镜像、工作区和运行时缓存
- 可访问模型 Provider 和聊天平台的外部网络
- Web 控制台或 Webhook 暴露到本机以外前，在反向代理中终止 TLS

镜像以 UID/GID `10001` 运行。`/data` 必须是持久化且可写的卷，用于保存配置、凭据、工作区、会话和日志。

## 部署

安装、备份、升级和安全说明参见 [Docker 部署指南](docker.zh.md)。
