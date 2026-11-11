# 定时任务与 Cron 作业

> 返回 [README](../README.md)

GoGoClaw 将定时作业存储在当前工作区，并可将它们作为提醒、完整 agent turn 或 shell 命令运行。

## 调度类型

GoGoClaw 目前在 cron 工具中使用三种调度形式：

- `at_seconds`：一次性作业，相对于当前时间。运行后，作业会从存储中删除。
- `every_seconds`：按秒计数的重复间隔。
- `cron_expr`：重复 cron 表达式，例如 `0 9 * * *`。

CLI 命令 `gogoclaw cron add` 目前只支持重复作业：

- `--every <seconds>`
- `--cron '<expr>'`

目前没有用于一次性 `at` 作业的 CLI 标志。

示例：

```bash
gogoclaw cron add --name "Daily summary" --message "Summarize today's logs" --cron "0 18 * * *"
gogoclaw cron add --name "Ping" --message "heartbeat" --every 300 --deliver
```

## Agent 工具操作

面向 agent 的 `cron` 工具支持以下操作：

- `add`：创建新作业。
- `list`：显示可访问的作业名称、ID 和调度。
- `get`：按 `job_id` 获取一个可访问的持久化作业，包括其保存的 payload。
- `update`：按 `job_id` 部分更新一个可访问的作业；省略的字段会保留。
- `remove`、`enable`、`disable`：现有管理操作。

重新调度现有任务时，请使用 `list -> get -> update`。不要仅仅为了修改调度而使用
`remove -> add`，因为重新创建作业可能会丢失原始 prompt、投递目标或命令 payload。

远程渠道访问限定于当前 `channel/chat_id`：远程调用方只能列出、获取或更新
已保存的 `payload.channel` 和 `payload.to` 与当前会话匹配的作业。命令作业包含 shell
命令 payload，因此只能从内部渠道或 `tools.cron.command_allowed_remotes` 允许的远程渠道
列出、检查或更新。

示例工具调用：

```json
{"action":"get","job_id":"79095b2f5685a0f2"}
```

```json
{"action":"update","job_id":"79095b2f5685a0f2","cron_expr":"30 10 * * *"}
```

`update` 接受 `name`、`message`、`command`，并且只接受一个调度字段
（`at_seconds`、`every_seconds` 或 `cron_expr`）。
省略 `command` 会保留它，将 `command` 设为非空字符串会替换它，将 `command`
设为 `""` 会清空它。命令更新需要与创建命令相同的渠道 allowlist 和确认门禁。

## 执行模式

作业以消息 payload 保存，并可通过三种稳定的用户可见模式执行：

### `deliver: false`

这是 cron 工具的默认模式。

当作业触发时，GoGoClaw 会将保存的消息作为新的 agent turn 送回 agent loop。这适用于可能需要推理、工具或生成回复的定时工作。

### `deliver: true`

当作业触发时，GoGoClaw 会将保存的消息直接发布到目标渠道和接收者，不经过 agent 处理。

CLI `gogoclaw cron add --deliver` 标志使用此模式。

### `command`

当 cron 工具作业包含 `command` 时，GoGoClaw 会通过 `exec` 工具运行该 shell 命令，并将命令输出发布回渠道。

对于命令作业，创建作业时 `deliver` 会被强制设为 `false`。保存的 `message` 只作为描述性文本；定时执行的动作是 shell 命令。

当前 CLI `gogoclaw cron add` 命令不暴露 `command` 标志。

## 配置与安全门禁

### `tools.cron`

`tools.cron.enabled` 控制是否注册面向 agent 的 `cron` 工具。默认值：`true`。

如果禁用 `tools.cron`，用户将无法再通过 agent 工具创建或管理作业。Gateway 仍会启动 `CronService`，但不会安装作业执行回调。因此，到期作业不会实际运行；一次性作业可能被删除，重复作业可能被重新调度但不执行其 payload。CLI 仍使用同一作业存储。

`tools.cron.exec_timeout_minutes` 设置定时命令执行使用的超时。默认值：`5`。设为 `0` 表示不超时。

### `tools.exec`

定时命令作业依赖 `tools.exec.enabled`。默认值：`true`。

如果 `tools.exec.enabled` 为 `false`：

- cron 工具会拒绝新的命令作业
- 现有命令作业触发时会发布 `command execution is disabled` 错误

`tools.exec.allow_remote` 仍由 exec 工具强制执行，但 cron 命令调度在创建作业时有其自己的渠道门禁。在实践中，提醒作业可以从远程渠道调度，而定时命令作业仅限于内部渠道和已配置的远程渠道。

### `allow_command`

`tools.cron.allow_command` 默认值为 `true`。

这不是硬性禁用开关。如果将 `allow_command` 设为 `false`，当调用方显式传入 `command_confirm: true` 时，GoGoClaw 仍允许命令作业。

命令作业还要求使用内部渠道或 `tools.cron.command_allowed_remotes` 允许的远程渠道。非命令提醒没有此限制。

### `command_allowed_remotes`

`tools.cron.command_allowed_remotes` 默认值为空列表。使用默认空列表时，远程渠道不能调度命令作业。

条目可以是渠道名称，或渠道加 chat id：

- `telegram` 允许来自任何 Telegram chat 的命令作业。
- `telegram:1234567890` 仅允许来自该确切 Telegram chat id 的命令作业。
- `*` 允许来自所有非空渠道的命令作业。

警告：`*` 可能有危险，因为任何能够与 GoGoClaw 对话的远程渠道都可以调度 shell 命令。
仅当所有启用的远程渠道和 chat 都可信地允许请求命令执行时才使用它。

此设置只控制远程渠道门禁。它不会绕过 `tools.cron.allow_command`、`command_confirm`、`tools.exec.enabled` 或 exec 工具的命令安全检查。

示例：

```json
{
  "tools": {
    "cron": {
      "enabled": true,
      "exec_timeout_minutes": 5,
      "allow_command": true,
      "command_allowed_remotes": [
        "telegram:1234567890"
      ]
    },
    "exec": {
      "enabled": true
    }
  }
}
```

## 持久化与位置

Cron 作业存储在：

```text
<workspace>/cron/jobs.json
```

默认工作区为：

```text
~/.gogoclaw/workspace
```

如果设置了 `GOGOCLAW_HOME`，默认工作区变为：

```text
$GOGOCLAW_HOME/workspace
```

Gateway 和 `gogoclaw cron` CLI 子命令都会使用同一个 `cron/jobs.json` 文件。

注意事项：

- 一次性 `at_seconds` 作业运行后会被删除
- 重复作业会保留在存储中，直到被移除
- 已禁用作业会保留在存储中，并且仍会出现在 `gogoclaw cron list` 中
