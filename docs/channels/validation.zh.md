# 渠道验证范围

CI 通过表示本地渠道行为及列出的测试服务通过；它不能证明每个真实平台账号、权限、凭据或线上 API 都正常。

| 验证层 | 覆盖内容 | 不覆盖内容 |
| --- | --- | --- |
| Go 渠道测试 | 使用进程内替身或本地 HTTP 服务验证适配器逻辑、请求映射、解析和错误处理 | 真实凭据、平台账号权限、网络策略或生产环境限流 |
| Docker 集成套件 | 验证 `integration/suites/` 下列出的真实服务连接；当前参考套件是 MCP Streamable | 聊天平台账号；该套件不会连接 Telegram、Discord、飞书、微信、QQ 或 Pico 服务 |
| Telegram 线上合约 | 使用配置的 Bot 凭据调用一次 Bot API `sendMessage`，并确认返回消息 ID | 长轮询和入站更新、群组权限、媒体上传、长期可用性或限流压力 |

常规 Go CI 会运行各渠道包的适配器测试。Telegram 还新增了本地 Bot API HTTP 合约测试：通过正式配置的 telego 客户端发送请求，并检查请求路径、目标会话、HTML 格式文本和返回消息 ID。消息拆分与格式转换由确定性单测覆盖；管理器重试由假渠道测试。

如需确认 Telegram 凭据和真实 API 调用，可手动运行 `Live Telegram contract` workflow。它会向配置的聊天发送一条带时间戳的可见文本消息。请将 `GOGOCLAW_LIVE_TELEGRAM_BOT_TOKEN` 配为仓库密钥，将 `GOGOCLAW_LIVE_TELEGRAM_CHAT_ID` 配为仓库变量，并选择适合接收测试消息的聊天。

其他平台目前有自动化本地测试，但没有使用真实凭据的 smoke workflow。只有在准备好专用测试账号并定义好单条消息或只读检查后，才应为对应平台添加可手动触发的线上合约。不会通过压力测试验证真实平台限流。
