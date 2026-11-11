# 评测 Harness

`pkg/harness` 通过确定性场景评测真实的 `AgentLoop` 运行。
它不是第二套 agent 实现：Provider、工具、会话持久化、
运行时事件和 turn 状态都走生产路径。

## 场景模型

一个 `Scenario` 可以定义：

- 同一会话中的一个或多个用户 turn。
- 脚本化 Provider 响应、错误、panic、延迟和上下文等待。
- turn 中途动态 steering 注入（`InjectSteering` / `SteerOnCall`）和
  中断（`InterruptKind: "graceful"` / `"hard"`、`InterruptOnCall`、
  `HardAbortOnCall`）。
- 普通、延迟、错误、panic、上下文取消和异步工具。
- 在已注册工具上自动接入 SubTurn spawner（`tools.NewSpawnTool`、
  `tools.NewDelegateTool`、`tools.NewSubagentTool`）。
- 在受检查的读写和 checkpoint 边界发生 session-store 故障。
- 异步 follow-up 处理以及 follow-up 发布期间的 bus 关闭。
- 用于重启/恢复场景的持久工作区。

`Result` 记录每个 turn 的模型调用、工具调用、运行时事件、终态、
历史、出站消息和排队的异步 follow-up。异步工具完成遵循与生产相同的
统一队列与 follow-up 规则。

## 断言

`Expect` 支持：

- 精确和子串形式的最终答案断言，包括精确空值。
- 精确/最小/最大模型调用次数，包括零次。
- Turn 数量、迭代次数（`iterations`）、状态、原因和完整阶段序列。
- 工具调用 ID、名称、参数、状态、错误和观察到的结果文本。
- 事件存在、有序子序列、精确序列、payload 子串和 turn ID。
- 每次模型调用的消息和工具列表断言。
- 持久化会话历史子串断言（`history_contains`）。
- 已发送出站 bus 消息断言（`outbound_contains`、`no_outbound`）。
- 持久化 golden-trace 比较。
- 工作区文件状态断言（`workspace_files`、`workspace_files_absent`），
  适用于成功标准是 artifact 或其他可观察最终状态的任务。
- suite 报告中 Provider 报告的 prompt、completion 和 total tokens。

默认情况下，错误会导致期望失败。使用 `error_contains` 声明预期失败，或者当错误本身不被匹配时使用 `allow_error`。

每次 `Verify` 调用还会检查结构不变量：每个 turn 各有一个 start 和 end 事件、`created` 生命周期起点、合法阶段转换、与 turn 状态匹配的终态阶段，以及没有工具启动缺少终态记录。

## Trace 持久化与回放

`Result.Snapshot`、`SaveTrace`、`LoadTrace` 和 `CompareTrace` 会为 golden 测试生成不带时间戳的 canonical trace。易变 ID、时长、工作区路径、时间戳和机器特定运行时细节会被移除。

`RecordPath` 在运行期间记录模型响应。`ReplayPath` 在返回每条记录的响应前检查模型、对话消息和工具名称。此输入检查会忽略机器特定 system prompt 文本；对话历史和工具面会被比较。

Golden trace 不带时间戳，并会规范化运行时 turn ID 和工作区路径。使用以下命令更新有意的 golden 变更：
`$env:UPDATE_HARNESS_GOLDEN='1'; go test ./pkg/harness -run TestGoldenBasicTurn`。

Go 场景可以提供真实 Provider。将其与状态断言或自定义测试期望配对，以根据可观察效果判断任务完成情况；脚本化 Provider 场景对于确定性的执行路径回归仍然有用。`ProductionIngress` 通过 message bus 和 `AgentLoop.Run` 发送测试消息，包括最终出站投递。`ConcurrentSetup` 可以在一个 loop 上运行多个会话，同时保留运行时事件 trace。

## 产品场景与模型行为评测

CI harness suite 包含产品路径场景：使用生产文件系统工具写入真实工作区 artifact、在 AgentLoop 实例之间重新打开 JSONL 会话存储，以及通过异步 follow-up 路径接收生产 `spawn` 工具结果。这些场景使用脚本化 Provider 响应，以保持回归确定性；它们不会给模型质量打分。

实时模型行为由
[`gogoclaw-model-eval`](../operations/model-behavior-evaluation.md) 单独评测。其版本化报告记录模型、任务结果、观察到的工具调用、artifact 摘要、最终答案和 Provider 报告的 token 用量。它会针对已配置的 OpenAI-compatible 端点运行一小组任务，并且采用 opt-in/manual 方式，使 PR CI 保持确定性且不产生 API 费用。

## Suite 与 CLI

`RunSuite` 和 `RunSuiteWithOptions` 返回结构化 `Report`，其中包含每个场景的通过/失败结果和 canonical trace。场景可以顺序运行，或使用有界并发运行（`SuiteOptions.Parallel`、`SuiteOptions.Concurrency`），并可通过正则过滤（`SuiteOptions.Filter`）。

独立 CLI 运行 JSON 场景，并在失败时以非零状态退出：

```bash
go run ./cmd/gogoclaw-harness \
  -scenario scenario.json \
  -filter "login.*" \
  -parallel -concurrency 4 \
  -v \
  -report report.json
```

`-scenario` 标志接受单个场景 JSON 文件、suite JSON 文件
（`{"scenarios": [...]}`）或包含 JSON 场景文件的目录。
支持的 JSON 工具类型有 `text`、`error`、`panic` 和 `async_text`。
JSON 场景还可以配置 `session_faults`（`get_history`、
`add_message`、`set_checkpoint`、`save` 等）、turn 中途 steering
（`inject_steering`）和中断（`interrupt_kind`），无需编写 Go 测试代码。
基于 Go 的自定义场景还可以使用 `SessionFault`、`ToolFactory`
和 `Setup`，用于 hook、MCP、channel 或其他集成特定故障。

## 命令

```bash
make harness-test
```

该目标在 race detector 下运行 harness，并且还会编译 CLI。
`agent scenarios` CI 作业会直接调用该目标。
