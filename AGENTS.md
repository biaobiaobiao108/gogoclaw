# 仓库开发与贡献指南 (Repository Guidelines)

## 项目概述与定位

GoGoClaw 是一个面向单机长期运行与私有化部署的智能 Agent 框架与 Web 控制台系统。它以单个可信实例为运行边界，不提供多租户、RBAC 或横向扩容能力；同一个数据目录只应由一个 GoGoClaw 实例管理。

- **运行形态**：生产环境**仅支持容器化部署**，本地电脑仅作开发与测试。
- **核心架构**：
  - **核心后端 (Go)**：提供模型协议适配、Agent 执行引擎、多通道即时通讯接入（飞书、钉钉、Telegram、MQTT 等）及统一 SQLite 持久化。
  - **管理控制台 (Web)**：嵌入式 React 19 单页应用，全链路使用 **Bun (1.4.4+)** 驱动管理，基于 TanStack Router 与 Tailwind CSS v4 构建。

## 项目结构与模块划分

- `cmd/gogoclaw`：CLI 核心入口与根命令定义。
- `pkg/`：核心后端模块，包括 Agent 循环、大模型 Providers、多通道接入、工具系统与 SQLite 存储。
- `web/backend`：Go 控制台后端服务（嵌入前端构建产物）。
- `web/frontend`：React 19 SPA（TanStack Router、Tailwind CSS v4、由 Bun 原生驱动）。
- `docker/`：多阶段生产构建 `Dockerfile` 与开发环境 Compose 配置。
- `docs/`：项目文档，仅保留英文基准文档与对应的 `.zh.md` 中文翻译。

## 构建、测试与开发常用命令

- `make build`：编译生成当前平台的 `gogoclaw` CLI 可执行文件。
- `make build-launcher`：编译前端并内嵌到控制台 Launcher 二进制中。
- `make dev`（或 `cd web && make dev`）：同时启动 Go 后端与前端开发服务（带 HMR 与接口代理）。
- `make test`：同时运行后端 Go 单元测试与前端 `bun test`。
- `make fmt`：使用 Go 原生 `gofmt` 格式化后端，使用 Bun 原生 `bun fmt` 格式化前端。
- `make docker-build`：构建生产容器镜像。

## 编码规范与风格约定

- **架构约束**：仅支持 64 位平台（`amd64` 与 `arm64`），严禁重新引入 32 位或淘汰架构。
- **Go 后端**：遵循官方标准格式，使用 `go fmt`；包名小写单数，导出标识符使用 PascalCase；不依赖外部 Linter。
- **前端规范**：全链路采用 Bun（1.4.4+），缩进 2 空格、无分号（`semi = false`）、双引号；保持 import 与 Tailwind 类名由 `bun fmt` 自动排序。
- **多语言支持**：仅保留中文与英文（`zh` / `en`），不引入其他语种。

## 测试要求

- **后端测试**：使用标准 `testing` 库，带 `-tags goolm,stdjson`；测试文件命名为 `*_test.go`。
- **前端测试**：使用 `bun:test`，测试文件统一命名为 `*.test.ts` / `*.test.tsx`。
- 提交前必须确保 `make test` 与 `make fmt` 全部通过。

## 提交规范与 Pull Request

- **改完即时中文提交**：每次完成代码修改与测试验证后，必须进行 Git 提交；**提交信息统一使用中文**，遵循 Conventional Commits 格式，例如：
  - `feat(models): 支持可配置的默认模型回退链`
  - `fix(tools): 修复文件写入覆盖提示的判断逻辑`
  - `refactor(web): 前端构建及工具链全面迁移至 Bun`
- **Pull Request**：简要说明改动内容与验证结果，关联对应 Issue，严禁提交临时构建产物。
