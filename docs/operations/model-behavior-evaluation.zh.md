# 模型行为评测

GoGoClaw 将两类评测分开：

- `make harness-test` 使用脚本化模型响应，验证确定性的控制流、持久化、工具行为和异步投递。
- `gogoclaw-model-eval` 向真实的 OpenAI 兼容模型发送少量请求，并检查可观察的任务结果。

模型接口连通性只证明服务端响应，不能算模型行为通过。行为报告的类型为 `model_behavior`，包含两个任务结果：

1. 要求模型调用生产 `write_file` 工具写入精确内容 `42`，评测器检查工具调用和实际文件字节。
2. 要求模型跨两轮记住一个代码，评测器将最终答案与预期代码比较。

每个任务使用独立工作区和会话。提示词与预期结果固定，便于比较同一模型的多次运行。模型采样仍可能变化，因此此工作流提供评测信号，不作为确定性的 PR 门禁。

## 配置与运行

配置以下环境变量：

| 变量 | 用途 |
| --- | --- |
| `GOGOCLAW_EVAL_API_BASE` | OpenAI 兼容 API 基址 |
| `GOGOCLAW_EVAL_API_KEY` | Provider API 密钥 |
| `GOGOCLAW_EVAL_MODEL` | 服务端接受的模型标识 |

本地运行并保存报告：

```bash
go run ./cmd/gogoclaw-model-eval -report build/evaluations/model-behavior.json
```

任一任务失败时，命令会以非零状态退出。报告不包含 API 密钥或原始请求头，但会包含模型最终回答和不匹配详情。若修改任务并加入私有信息，请勿公开报告。

GitHub Actions 中的 `Model behavior evaluation` workflow 需要手动触发，并会将 JSON 报告保存为 artifact。运行前需在仓库配置上述变量和密钥。
