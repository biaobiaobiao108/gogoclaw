# MCP 服务 CLI

> 返回 [README](../README.md)

GoGoClaw 包含一个 `mcp` CLI 命令组，用于管理 `config.json` 中的 MCP 服务条目。

此 CLI 充当**配置管理器**：

- 它添加、更新、移除并校验 `tools.mcp.servers` 下的条目
- 它自身**不会**让 MCP 服务保持运行
- 当 MCP 启用时，gateway / host 仍会启动配置的服务

## 写入位置

CLI 更新的是 GoGoClaw 其他部分使用的同一个配置文件：

- 如果设置了 `GOGOCLAW_CONFIG`，则使用该文件
- 否则使用 `~/.gogoclaw/config.json`

CLI 写入文件时，它会：

- 原子保存
- 保留 GoGoClaw 使用的标准 2 空格 JSON 格式
- 在写入前校验生成的 JSON

行为说明：

- `gogoclaw mcp add ...` 会启用 `tools.mcp.enabled`
- 使用 `gogoclaw mcp remove ...` 移除最后一个服务时，会禁用 `tools.mcp.enabled`

## 快速开始

通过 `bunx` 添加一个 stdio 服务：

```bash
gogoclaw mcp add filesystem -- bunx --bun @modelcontextprotocol/server-filesystem /tmp
```

添加一个 stdio 服务，并将环境变量保存在配置中：

```bash
gogoclaw mcp add github --env GITHUB_PERSONAL_ACCESS_TOKEN=ghp_xxx -- bunx --bun @modelcontextprotocol/server-github
```

使用 env 文件为密钥添加一个 stdio 服务：

```bash
gogoclaw mcp add github --env-file .env.github -- bunx --bun @modelcontextprotocol/server-github
```

添加一个远程 HTTP 服务：

```bash
gogoclaw mcp add context7 --transport http https://mcp.context7.com/mcp
```

添加一个带认证头的远程 HTTP 服务，即使标志位于 URL 之后：

```bash
gogoclaw mcp add apify "https://mcp.apify.com/" -t http --header "Authorization: Bearer OMITTED"
```

使用显式命令分隔符添加一个 stdio 服务：

```bash
gogoclaw mcp add --transport stdio --env AIRTABLE_API_KEY=YOUR_KEY airtable -- bunx --bun airtable-mcp-server
```

检查已配置的条目：

```bash
gogoclaw mcp list
gogoclaw mcp list --status
```

查看一个服务的完整详情及其暴露的工具：

```bash
gogoclaw mcp show filesystem
```

探测单个服务条目：

```bash
gogoclaw mcp test filesystem
```

打开原始配置进行高级编辑：

```bash
gogoclaw mcp edit
```

## 命令摘要

| 命令 | 用途 |
|---------|---------|
| `gogoclaw mcp add <name> [flags] <command-or-url> [args...]` | 添加或更新 MCP 服务条目 |
| `gogoclaw mcp remove <name>` | 从配置中移除服务条目 |
| `gogoclaw mcp list` | 列出已配置的 MCP 服务 |
| `gogoclaw mcp show <name>` | 显示一个服务的完整详情和工具 |
| `gogoclaw mcp test <name>` | 尝试连接一个已配置的服务 |
| `gogoclaw mcp edit` | 在 `$EDITOR` 中打开 `config.json` |

## `gogoclaw mcp add`

语法：

```bash
gogoclaw mcp add <name> [flags] <command-or-url> [args...]
```

支持的标志：

| 标志 | 含义 |
|------|---------|
| `--env`, `-e` | 以 `KEY=value` 格式添加 stdio 环境变量。可重复。值会保存到配置。 |
| `--env-file` | 为 stdio 服务附加 env 文件路径。推荐用于不希望以内联方式存入 `config.json` 的密钥。 |
| `--header`, `-H` | 以 `Name: Value` 或 `Name=Value` 格式添加 HTTP 头。可重复。 |
| `--transport`, `-t` | 传输类型：`stdio`（默认）、`http` / `streamable-http` 或 `sse`。 |
| `--force`, `-f` | 无需确认即可覆盖现有服务条目。 |

所有启用的服务都使用自动工具发现。

支持的形式：

```bash
gogoclaw mcp add [flags] <name> <command-or-url> [args...]
gogoclaw mcp add [flags] <name> -- <command> [args...]
```

解析行为：

- CLI 标志可以出现在名称之前、名称与目标之间，或远程传输的 URL 之后
- 对于 `stdio`，最稳健的形式是 `-- <command> [args...]`
- 当 stdio 命令本身带有看起来像 GoGoClaw CLI 标志的参数时，请使用 `--` 分隔符
- 不使用 `--` 时，GoGoClaw 将前两个非标志 token 视为 `<name>` 和 `<command-or-url>`

密钥处理：

- `--env KEY=value` 会将解析后的值直接存入 `config.json`
- 当值敏感且应留在主配置文件之外时，请改用 `--env-file`

示例：

```bash
gogoclaw mcp add sqlite bunx --bun @modelcontextprotocol/server-sqlite --db ./mydb.db
```

这会存储：

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "sqlite": {
          "enabled": true,
          "type": "stdio",
          "command": "bunx",
          "args": ["--bun", "@modelcontextprotocol/server-sqlite", "--db", "./mydb.db"]
        }
      }
    }
  }
}
```


### 添加命令规则

对于 `stdio`：

- `<command-or-url>` 被视为命令
- `[args...]` 存储在 `args` 中
- 支持 `--env`
- 支持 `--env-file`，并存储在 `env_file` 中
- 拒绝 `--header`
- 支持 `-- <command> [args...]`，并推荐用于无歧义解析

对于 `http` / `streamable-http` / `sse`：

- `<command-or-url>` 必须是有效 URL
- 拒绝额外的命令参数
- 拒绝 `--env`
- 拒绝 `--env-file`
- 支持 `--header`，并存储在 `headers` 中
- `http` 和 `streamable-http` 使用 streamable HTTP 请求-响应模式
- `sse` 使用相同的 streamable HTTP 传输，但同时启用可选的独立 SSE 监听器，用于服务端发起的通知

覆盖行为：

- 如果 `<name>` 已存在，GoGoClaw 会要求确认
- 使用 `--force` 跳过提示

本地路径校验：

- 如果命令看起来像本地路径，例如 `./server.py` 或 `/opt/mcp/server`
- GoGoClaw 会检查文件是否存在
- 在非 Windows 平台上，它还会检查文件是否可执行

清晰的 URL/传输错误：

- 如果目标看起来像 `https://...` 但传输仍为 `stdio`，GoGoClaw 会返回明确错误，提示你使用 `--transport http` 或 `--transport sse`

## `gogoclaw mcp remove`

语法：

```bash
gogoclaw mcp remove <name>
```

这会从 `tools.mcp.servers` 中移除指定条目。

如果移除的服务是最后一个已配置的 MCP 服务，GoGoClaw 还会禁用 `tools.mcp.enabled`。

## `gogoclaw mcp list`

语法：

```bash
gogoclaw mcp list
gogoclaw mcp list --status
```

在宽终端中，输出是一个样式化框（外观与 `mcp show` 相同）。在窄终端中或 stdout 不是 TTY 时，会改为打印纯 ASCII 表格。

输出字段：

| 字段 | 含义 |
|-------|---------|
| `Name` | `tools.mcp.servers` 内的服务键 |
| `Type` | 实际传输：`stdio`、`http` 或 `sse` |
| `Command` / `Target` | stdio 服务保存的命令行，或远程服务的 URL |
| `Status` | 默认显示 `enabled` / `disabled`；使用 `--status` 时显示 `ok (N tools)` 或 `error` |

注意事项：

- 不使用 `--status` 时，GoGoClaw 只打印配置状态
- 使用 `--status` 时，GoGoClaw 会尝试连接每个启用的服务，并报告 `ok (N tools)` 或 `error`
- 要查看服务暴露的完整工具列表，请使用 `gogoclaw mcp show <name>`

## `gogoclaw mcp show`

语法：

```bash
gogoclaw mcp show <name>
gogoclaw mcp show <name> --timeout 15s
```

这会连接到指定服务并打印：

- 服务元数据：名称、传输类型、目标、启用状态、环境变量名、env 文件、头名称
- 服务暴露的每个工具，及其名称、说明和参数（名称、类型、必需/可选、说明）

在宽终端中，输出是与 `mcp list` 外观一致的样式化框。在窄终端或非 TTY stdout 中，会改为打印纯文本。

示例输出（宽终端）：

```
╭──────────────────────────────────────────────────────────╮
│ ⬡  filesystem                                            │
│                                                          │
│ Type        stdio                                        │
│ Target      bunx --bun @modelcontextprotocol/server-fs /tmp  │
│ Enabled     yes                                          │
│ Deferred    no                                           │
│                                                          │
│ Tools (3)                                                │
│                                                          │
│   read_file  [1/3]                                       │
│   Read the complete contents of a file from the disk     │
│                                                          │
│     path  <string>  required                             │
│       Path to the file to read                           │
│ ──────────────────────────────────────────────────────── │
│   ...                                                    │
╰──────────────────────────────────────────────────────────╯
```

标志：

| 标志 | 默认值 | 含义 |
|------|---------|---------|
| `--timeout` | `10s` | 连接超时 |

注意事项：

- 如果服务在配置中已禁用，`mcp show` 只打印元数据并跳过工具发现
- `mcp show` 总是实时连接以获取工具列表；如果只需要可达性检查，请使用 `mcp test`

## `gogoclaw mcp test`

语法：

```bash
gogoclaw mcp test <name>
```

这会为一个已配置条目执行直接连接测试，并在成功时打印发现的工具数量。

它适用于：

- 在启动 gateway 前验证新添加的服务
- 调试单个服务而不探测整个列表
- 条目当前在配置中已禁用，但仍想验证其定义

## `gogoclaw mcp edit`

语法：

```bash
gogoclaw mcp edit
```

这会使用 `$EDITOR` 指向的编辑器打开配置文件。

当需要配置 `gogoclaw mcp add` 未直接暴露的 MCP 字段时使用它。

如果未设置 `$EDITOR`，该命令会以明确错误失败。

## 推荐工作流

对于常见场景：

1. 使用 `gogoclaw mcp add` 添加服务。
2. 使用 `gogoclaw mcp show <name>` 验证连接并检查暴露的工具。
3. 使用 `gogoclaw mcp list --status` 一眼检查所有服务。
4. 正常启动 GoGoClaw，使配置的 MCP 服务由 host 加载。

对于高级场景：

1. 使用 `gogoclaw mcp add` 添加基础条目。
2. 运行 `gogoclaw mcp edit` 填写未作为 CLI 标志暴露的字段。
3. 运行 `gogoclaw mcp show <name>` 确认最终配置和工具列表。

## 相关文档

- [工具配置](tools_configuration.md#mcp-tool)：MCP 配置结构、传输、发现和示例
- [README](../README.md)：高层概览
