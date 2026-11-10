# Agent 正确性、性能与成本治理

GoGoClaw 把模型用量、预算和工具执行结果作为运行事实处理，而不是只依赖模型自述。

## Token 预算

预算位于 `agents.defaults.usage_governance`：

```json
{
  "agents": {
    "defaults": {
      "usage_governance": {
        "session_token_budget": 2000000,
        "daily_token_budget": 10000000,
        "on_budget_exceeded": "stop"
      }
    }
  }
}
```

- `0` 表示关闭对应预算，现有部署不会因为升级突然被限制。
- 每次 Agent 回合中的模型请求前，包括重试、备用模型和摘要请求，都会检查“已记录的 provider 用量 + 同一进程中在途请求的预留 + 本次输入估算 + `max_tokens` 输出预留”。超过预算时不发起请求。
- `stop` 是默认策略；`warn` 只适合明确接受继续消耗的安装。
- 会话预算包含子任务上报给父会话的用量。每日预算按 GoGoClaw 进程所在时区的自然日聚合，并覆盖同一实例内的全部 Agent。
- 每日聚合写入默认工作区的 `sessions.db`，作为实例级账本；其他 Agent 工作区只保存自己的会话预算桶。
- 配置了预算但会话存储不能记账时，运行时会失败关闭，不会无计量地继续调用模型。

如果 provider 没有返回 usage，该次调用无法得到可验证的 token 数；请求前预检仍会阻止已知超限调用，但事后聚合只记录 provider 实际返回的数据。

## 用量和成本

模型条目可配置本地估算单价：

```json
{
  "model_name": "fast-model",
  "provider": "openai",
  "model": "gpt-test",
  "input_cost_per_million_usd": 0.15,
  "output_cost_per_million_usd": 0.6
}
```

单价只用于本地报表，不会发送给 provider。聚合结果保存于实例级 `sessions.db`，按 Agent、频道、Provider、模型、模型 ID 和自然日拆分。成本以整数纳美元累计，既能避免长时间运行后的浮点漂移，也不会把低频短调用四舍五入成零。Web 控制台的“用量与成本”页面展示每日趋势和四个维度。

## 工具结果与最终结论

会改变状态的工具如果最后一次执行失败或结果未知，最终回复会追加明确的验证提示，回合结束原因标记为 `tool_failure`。成功重试只覆盖同一工具、同一参数指纹的旧失败；不同操作仍各自保留结果。只读工具失败不会强制追加该提示。

策略拒绝不会执行外部操作。如果最终回复已经明确写出拒绝原因，不再重复追加提示；如果模型仍声称任务完成，则追加“操作已被拒绝”的验证提示。steering 主动跳过、执行前取消等未产生副作用的情况不会误报成副作用失败。

这不能证明外部系统一定处于期望状态，但可以保证 GoGoClaw 不会把没有成功工具证据的结果包装成无保留的成功结论。

## 发布门槛

提交前至少运行：

```bash
go test -tags goolm,stdjson ./pkg/agent ./pkg/memory ./pkg/session ./web/backend/api
go test -race -tags goolm,stdjson ./pkg/agent ./pkg/memory ./pkg/session ./pkg/harness/...
make harness-test
```

Harness 报告中的 `model_latency_ms` 和 `turn_latency_ms` 提供模型调用与回合完成时间的 P50/P95。发布负责人应结合部署硬件和 provider 区域设置门槛；仓库不写死跨环境无法成立的绝对毫秒数。当前固定场景覆盖会话重启、工具中断、并行工具、异步结果和 fallback 路径；审批恢复与长上下文摘要仍由 `pkg/agent` 单测覆盖，尚未全部进入 Harness 夹具。

10,000 条消息的首屏查询基线：

```bash
go test -tags goolm,stdjson ./pkg/memory -run '^$' -bench BenchmarkSQLiteStoreTranscriptPage10000 -benchmem -count=5
```

## 运维边界

- 用量表是聚合事实，不保存提示词或工具输出。
- 删除会话会删除该会话的预算桶；每日聚合保留，用于系统级成本报表。
- 预算单位是 token，不是货币。金额预算需要 provider 返回稳定 usage 且模型价格配置完整后再引入。
- 在途预留在进程内共享。当前预算不具备跨多个 GoGoClaw 实例共享的能力；同一个数据目录仍只应由一个 GoGoClaw 实例管理。
