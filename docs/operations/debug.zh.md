# 调试 GoGoClaw

> 返回 [README](../project/README.zh.md)

GoGoClaw 在处理每一个请求时，都会在后台执行多个复杂的交互操作——从消息路由和复杂度评估，到工具执行和模型故障适配。能够准确地看到正在发生什么至关重要，这不仅有助于排查潜在问题，也有助于真正理解代理的运作方式。

## 文件日志与轮转

设置 `GOGOCLAW_LOG_FILE` 后，日志会写入文件；除非运行时关闭控制台日志，否则同一份日志仍会显示在控制台。

| 环境变量 | 默认值 | 用途 |
|---|---:|---|
| `GOGOCLAW_LOG_MAX_SIZE_MB` | `10` | 当前日志达到此大小时轮转；设为 `0` 可关闭大小轮转 |
| `GOGOCLAW_LOG_ROTATE_HOURS` | `24` | 当前日志写入达到此小时数后轮转；设为 `0` 可关闭时间轮转 |
| `GOGOCLAW_LOG_MAX_BACKUPS` | `5` | 保留的轮转日志文件数量 |
| `GOGOCLAW_LOG_MAX_AGE_DAYS` | `30` | 删除超过此天数的轮转日志 |

默认配置会把活动日志控制在约 10 MB 以内，并最多保留 5 个最近备份、保留 30 天。

## 以调试模式启动 GoGoClaw

要获取代理运行的详细信息（LLM 请求、工具调用、消息路由），可以使用调试标志启动 GoGoClaw 网关：

```bash
gogoclaw gateway --debug
# or
gogoclaw gateway -d
```

在此模式下，系统会对日志进行详细格式化，并显示系统提示词和工具执行结果的预览。

## 禁用日志截断（完整日志）

默认情况下，GoGoClaw 会在调试日志中截断过长的字符串（例如*系统提示词*或大型 JSON 输出结果），以保持控制台的可读性。

如果你需要检查某个命令的完整输出，或发送给 LLM 模型的确切载荷，可以使用 `--no-truncate` 标志。

**注意：** 此标志*仅*在与 `--debug` 模式组合使用时有效。

```bash
gogoclaw gateway --debug --no-truncate

```

当此标志激活时，全局截断功能将被禁用。这在以下场景中非常有用：

* 验证发送给提供商的消息的确切语法。
* 读取 `exec`、`web_fetch` 或 `read_file` 等工具的完整输出。
* 调试保存在内存中的会话历史。

## SQLite 启动完整性

进程在读写会话数据之前，会通过 `PRAGMA quick_check(1)` 检查 `sessions/sessions.db`。Schema 初始化在事务中执行；检查失败时会拒绝打开数据库，而不是带着损坏状态继续运行。

需要完整检查、备份或恢复时，请使用 `gogoclaw storage` 命令，参见 [SQLite 存储恢复](storage-recovery.zh.md)。

## 长时运行 soak

`TestProductSoak` 默认跳过，设置时长后启用：

```bash
GOGOCLAW_SOAK_DURATION=20m make soak-test
```

测试会持续运行多轮对话，并输出 CSV，记录 RSS、goroutine、Go heap/sys、SQLite、WAL 与日志占用。可用 `GOGOCLAW_SOAK_REPORT` 指定 CSV 路径；可选阈值为 `GOGOCLAW_SOAK_MAX_RSS_GROWTH_MB` 与 `GOGOCLAW_SOAK_MAX_GOROUTINE_GROWTH`，Linux 下会报告 RSS。

可靠性工作流会运行 20 分钟 soak 并上传报告 artifact，不包含 NAS 72 小时测试。
