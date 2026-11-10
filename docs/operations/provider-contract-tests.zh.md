# 可选的真实模型服务契约检查

普通 CI 使用本地模拟服务，不依赖账号或密钥。若要验证真实的 OpenAI-compatible 服务，可手动运行 GitHub Actions 的 `Live provider contract` 工作流；它只发起一次极短的聊天请求，检查认证、请求路径和响应解析是否能与当前服务正常对接。

首次使用前，在仓库设置中配置：

| 类型 | 名称 | 内容 |
| --- | --- | --- |
| Repository variable | `GOGOCLAW_LIVE_LLM_API_BASE` | OpenAI-compatible API 基址，例如包含 `/v1` 的地址 |
| Repository variable | `GOGOCLAW_LIVE_LLM_MODEL` | 服务端接受的模型标识 |
| Repository secret | `GOGOCLAW_LIVE_LLM_API_KEY` | API 密钥 |

然后在 Actions 中手动运行 `Live provider contract`，明确勾选 `run_live_contract`。未勾选时不会向模型服务发送请求。真实请求可能产生少量费用；请使用有低额度限制的测试密钥。工作流固定使用仓库变量中的地址，避免把密钥发送到手动输入的任意主机。

本检查覆盖 OpenAI-compatible 聊天完成接口。它不替代 Telegram、Discord 等渠道的端到端测试，也不能保证兼容 API 的所有扩展参数均可用。
