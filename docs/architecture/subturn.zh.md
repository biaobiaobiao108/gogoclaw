# 🔄 SubTurn 机制

> 返回 [README](../README.md)

## 概述

`SubTurn` 机制是 GoGoClaw 的核心功能，允许工具生成隔离的、嵌套的 agent loop 来处理复杂子任务。

通过使用 SubTurn，agent 可以拆解问题，并在独立的临时会话中运行一次单独的 LLM 调用。这确保中间推理、后台任务或子代理输出不会污染主对话历史。

## 核心能力

- **上下文隔离**：每个 SubTurn 使用 `ephemeralSessionStore`。其消息历史不会泄漏到父任务中，并会在完成时销毁。临时会话最多保存 **50 条消息**；达到此限制时，较旧消息会自动截断。
- **深度与并发限制**：防止无限循环和资源耗尽。
  - **最大深度**：最多 3 层嵌套。
  - **最大并发**：每个父 turn 最多 5 个并发 sub-turn（通过带 30 秒超时的信号量管理）。
- **上下文保护**：支持软上下文限制（`MaxContextRunes`）。它会在命中 Provider 硬上下文窗口限制前，主动截断旧消息（同时保留 system prompt 和近期上下文）。
- **错误恢复**：通过压缩历史并重试，自动检测并恢复 Provider 上下文长度超限错误和截断错误。

## 配置（`SubTurnConfig`）

生成 SubTurn 时，必须提供 `SubTurnConfig`：

| 字段 | 类型 | 说明 |
| :--- | :--- | :--- |
| `Model` | `string` | 用于 sub-turn 的 LLM 模型（例如 `gpt-4o-mini`）。**必需。** |
| `Tools` | `[]tools.Tool` | 授予 sub-turn 的工具。如果为空，则继承父级工具。 |
| `SystemPrompt` | `string` | sub-turn 的任务描述。作为第一条用户消息发送给 LLM（不是作为 system prompt 覆盖）。 |
| `ActualSystemPrompt` | `string` | 可选，用于替换 agent 默认 system prompt 的显式 system prompt。留空则继承父 agent 的 system prompt。 |
| `MaxTokens` | `int` | 生成响应的最大 token 数。 |
| `Async` | `bool` | 控制结果是否也通过统一异步投递路径入队。 |
| `Critical` | `bool` | 如果为 `true`，即使父级优雅完成，sub-turn 仍会继续运行。 |
| `Timeout` | `time.Duration` | 最大执行时间（默认：5 分钟）。 |
| `MaxContextRunes`| `int` | 软上下文限制。`0` = 自动计算（模型上下文窗口的 75%，推荐），`-1` = 无限制（禁用软截断，仅依赖硬上下文错误恢复），`>0` = 使用指定的 rune 限制。 |

> **注意：** `Async` 标志**不会**让调用变为非阻塞。它只控制结果是否也为父 turn 入队。两种模式都会阻塞调用方，直到 sub-turn 完成。要实现真正的非阻塞执行，调用方必须在单独的 goroutine 中生成 sub-turn。

## 执行模式

### 同步（`Async: false`）

这是调用方需要立即获得结果才能继续的标准模式。

- 调用方会阻塞，直到 sub-turn 完成。
- 结果**只**通过函数返回值直接返回。
- 不会排队任何异步投递。

**示例：**
```go
cfg := agent.SubTurnConfig{
    Model:        "gpt-4o-mini",
    SystemPrompt: "Analyze the provided codebase...",
    Async:        false,
}
result, err := agent.SpawnSubTurn(ctx, cfg)
// Process result immediately
```

### 异步（`Async: true`）

用于“发射后不管”的操作或并行处理，父 turn 稍后收集结果。

- 结果通过统一异步投递队列投递。
- 结果**也**通过函数返回值返回（为保持一致性）。
- 父 turn 在下一次模型调用前恰好消费一次队列，并将结果作为 `[SubTurn Result]` 注入。

**示例：**
```go
cfg := agent.SubTurnConfig{
    Model:        "gpt-4o-mini",
    SystemPrompt: "Run a background security scan...",
    Async:        true,
}
result, err := agent.SpawnSubTurn(ctx, cfg)
// The result will also be injected into the parent loop later via channel
```

## 错误恢复与重试

SubTurn 为瞬时错误实现自动重试机制：

| 错误类型 | 最大重试次数 | 恢复动作 |
|:-----------|:------------|:----------------|
| 上下文长度超限 | 2 | 强制压缩历史并重试 |
| 响应被截断（`finish_reason="truncated"`） | 2 | 注入恢复 prompt 并重试 |

### 截断恢复
当 LLM 响应被截断（`finish_reason="truncated"`）时，SubTurn 会自动：
1. 从 `turnState.lastFinishReason` 检测截断
2. 注入恢复 prompt："Your previous response was truncated due to length. Please provide a shorter, complete response..."
3. 最多重试 2 次

### 上下文错误恢复
当 Provider 返回上下文长度错误（例如 `context_length_exceeded`）时：
1. 强制压缩消息历史（丢弃最旧的 50% 对话）
2. 使用压缩后的上下文重试
3. 失败前最多重试 2 次

## 生命周期与取消

SubTurn 在独立上下文中运行，但与其父 `turnState` 保持结构关联。

### 父级优雅完成
当父任务自然完成（`Finish(false)`）时：
- **非关键** sub-turn 会收到信号，优雅退出而不抛出错误。
- **关键**（`Critical: true`）sub-turn 会在后台继续运行。完成后，其结果会作为 **Orphan Results** 发出，以免数据丢失。

### 硬中止
当父任务被强制中止时（例如用户使用 `/stop` 中断）：
- 触发级联取消，立即终止所有子级和孙级 sub-turn。
- 根 turn 的会话历史会回滚到 turn 开始时（`initialHistoryLength`）的快照，防止脏上下文。SubTurn 不受此回滚影响，因为它们使用临时会话，最终都会被丢弃。

## Agent Loop 集成

### 消息路由与 Steering

当消息进入 `Run()` 循环时，agent 决定是启动新 worker 还是入队到 steering：

- 如果消息的会话键**没有活动 turn**，则原子预留会话并启动 **worker goroutine**。worker 处理完整 turn 生命周期：`processMessage` → 工具执行 → steering drain → 对排队消息执行 `Continue`。
- 如果同一会话**已存在活动 turn**，消息直接进入该会话的 steering 队列。它会被现有 worker 的 steering drain 循环取走。

这确保：
- 来自**不同会话**的消息会**并行**处理（最多 `max_parallel_turns` 个并发 worker）
- 来自**同一会话**的消息会严格**串行化** —— 它们进入 steering 队列，并在活动 turn 内按顺序处理
- 不需要后台 drain goroutine；steering 由 worker 在处理后自行处理

### 统一异步投递

Coordinator 在每次模型调用前恰好排空所属 turn 的统一异步投递队列一次。工具结果和 SubTurn 结果使用同一队列；pipeline 阶段不维护私有轮询 channel。在 turn 停止消费后到达的结果会成为 follow-up 入站 turn。

### Turn 状态跟踪

所有活动 turn 都注册在 `AgentLoop.activeTurnStates`（`sync.Map`，以会话键为键）。worker 启动前会通过 `LoadOrStore` 原子存储一个预留 sentinel，然后在 `runTurn` 注册时替换为真实 `*turnState`。这可以防止同一会话的多条消息生成并发 worker 的 TOCTOU 竞态。sentinel 由 worker 的延迟清理负责清除。这允许 `HardAbort` 和 `/subagents` 可观测性命令找到并操作活动 turn。

## 运行时事件集成

SubTurn 通过 `pkg/events` 发出运行时事件，用于可观测性和调试：

| 事件类型 | 发出时机 | Payload |
|:------|:-------------|:--------|
| `agent.subturn.spawn` | Sub-turn 成功初始化 | `SubTurnSpawnPayload{AgentID, Label, ParentTurnID}` |
| `agent.subturn.end` | Sub-turn 完成（成功或错误） | `SubTurnEndPayload{AgentID, Status}` |
| `agent.subturn.result_delivered` | 异步结果成功投递给父级 | `SubTurnResultDeliveredPayload{TargetChannel, TargetChatID, ContentLen}` |
| `agent.subturn.orphan` | 结果无法投递（父级已完成或 channel 已满） | `SubTurnOrphanPayload{ParentTurnID, ChildTurnID, Reason}` |

## API 参考

### SpawnSubTurn（公共入口）

```go
func SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*tools.ToolResult, error)
```

这是供 agent 内部代码（例如测试、直接调用）使用的导出包级入口。它从 context 获取 `AgentLoop` 和 `turnState`，并委托给内部 `spawnSubTurn`。

**要求：**
- 必须通过 `WithAgentLoop()` 将 `AgentLoop` 注入 context
- context 中必须存在父 `turnState`（从工具调用时自动设置）

**返回：**
- `*tools.ToolResult`：包含带有 sub-turn 输出的 `ForLLM` 字段
- `error`：已定义错误类型之一或 context 错误

### AgentLoopSpawner（接口实现）

```go
type AgentLoopSpawner struct { al *AgentLoop }

func (s *AgentLoopSpawner) SpawnSubTurn(ctx context.Context, cfg tools.SubTurnConfig) (*tools.ToolResult, error)
```

这为需要生成 sub-turn 但不想直接 import `agent` 包的工具实现 `tools.SubTurnSpawner` 接口（避免循环依赖）。它在委托给内部 `spawnSubTurn` 前将 `tools.SubTurnConfig` → `agent.SubTurnConfig`。

### NewSubTurnSpawner

```go
func NewSubTurnSpawner(al *AgentLoop) *AgentLoopSpawner
```

为给定 AgentLoop 创建新的 spawner 实例。在工具注册期间将返回值传给 `SpawnTool.SetSpawner()` 或 `SubagentTool.SetSpawner()`。

### Continue

```go
func (al *AgentLoop) Continue(ctx context.Context, sessionKey, channel, chatID string) (string, error)
```

通过为给定会话出队 steering 消息并将其运行通过 agent loop，恢复空闲 agent turn。如果发生了处理，则返回响应字符串；如果没有待处理 steering 消息，则返回空字符串。它使用会话感知的活动 turn 检查——只在*同一*会话有活动 turn 时阻塞，不会因无关会话而阻塞。

## 上下文传播

SubTurn 依赖 context 值来正确运行：

| Context Key | 用途 |
|:------------|:--------|
| `agentLoopKey` | 存储 `*AgentLoop`，供工具访问和 SubTurn 生成使用 |
| `turnStateKey` | 存储 `*turnState`，用于层次跟踪和结果投递 |

### 注入依赖

```go
// Before calling tools that may spawn SubTurns
ctx = WithAgentLoop(ctx, agentLoop)
ctx = withTurnState(ctx, turnState)
```

### 独立子上下文

**重要**：子 SubTurn 使用从 `context.Background()` 派生的**独立上下文**，而不是父 context。这个设计选择：

- 允许关键 SubTurn 在父级取消后继续
- 防止父超时影响子执行
- 子级有自己的超时用于自我保护（`Timeout` 配置或默认 5 分钟）

## 错误类型

| 错误 | 条件 |
|:------|:----------|
| `ErrDepthLimitExceeded` | SubTurn 深度超过 3 层 |
| `ErrInvalidSubTurnConfig` | 必需字段 `Model` 为空 |
| `ErrConcurrencyTimeout` | 所有 5 个并发槽位被占用超过 30 秒 |
| Context errors | 获取信号量期间父 context 被取消 |

## 线程安全

SubTurn 为并发执行而设计：

- **父子关系**：在 mutex 下管理（`parentTS.mu.Lock()`）
- **活动 turn 跟踪**：使用 `sync.Map` 并发访问 `activeTurnStates`
- **ID 生成**：使用 `atomic.Int64` 生成唯一 SubTurn ID（格式：`subturn-N`，每个 `AgentLoop` 实例全局单调递增）
- **结果投递**：所有生产者推入父级的统一异步投递队列。队列永不阻塞；溢出会转换为 follow-up turn。

## 异步结果投递

Sub-turn 和异步工具共享一种投递模型：

1. 活动 turn 在其异步投递队列中接收结果。
2. turn 在下一次模型调用前消费一次队列。
3. 如果 turn 已经停止，或队列已满，结果会被暂存，直到 turn 注销，然后作为 follow-up 入站 turn 发布。
4. 硬中止会丢弃结果，并为 sub-turn 发出 `agent.subturn.orphan`。

Follow-up 投递是无损的，且永不阻塞生产者。16 项队列是快速路径，不是持久性边界。

## 工具继承

### 当 `cfg.Tools` 为空时：
- SubTurn 继承父 agent 的**所有**工具
- 工具注册在新的 `ToolRegistry` 实例中
- Tool TTL 独立于父级管理

### 当指定 `cfg.Tools` 时：
- SubTurn 只能使用指定的工具
- 父级工具**不会**合并
- 使用此方式出于安全或聚焦目的限制 SubTurn 能力

**示例 - 受限 SubTurn：**
```go
cfg := agent.SubTurnConfig{
    Model: "gpt-4o-mini",
    Tools: []tools.Tool{readOnlyTool}, // Only read-only access
    SystemPrompt: "Analyze the file structure...",
}
```

## 参考

| 常量 | 值 |
|:---------|:------|
| `maxSubTurnDepth` | 3 |
| `maxConcurrentSubTurns` | 5 |
| `concurrencyTimeout` | 30s |
| `defaultSubTurnTimeout` | 5m |
| `maxEphemeralHistorySize` | 50 messages |
| Async delivery queue | 16 |
| `MaxContextRunes` default | 75% of model context window |
