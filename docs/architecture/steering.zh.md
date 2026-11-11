# Steering

Steering 允许向已经运行的 agent loop 注入消息，在工具调用之间中断它，而不必等待整个周期完成。

## 工作原理

当 agent 正在执行一串工具调用（例如模型在单个 turn 中请求了 3 个工具）时，steering 会在**每个工具**完成后检查队列。如果发现排队消息：

1. 剩余工具会被**跳过**，并将 `"Skipped due to queued user message."` 作为它们的结果
2. steering 消息会被**注入到对话上下文**
3. 使用更新后的上下文再次调用模型，其中包括用户的 steering 消息

```
User ──► Steer("change approach")
                │
Agent Loop      ▼
  ├─ tool[0] ✔  (executed)
  ├─ [polling] → steering found!
  ├─ tool[1] ✘  (skipped)
  ├─ tool[2] ✘  (skipped)
  └─ new LLM turn with steering message
```

## 按作用域隔离的队列

Steering 现在按解析后的会话作用域隔离，而不是存储在单个全局队列中。

- 活动 turn 从自己的作用域键（通常是路由后的会话键，例如 `agent:<agent_id>:...`）读写
- 在活动 turn 之外，`Steer()` 仍通过旧版回退队列工作
- `Continue()` 首先为请求的会话作用域出队消息，然后回退到旧版队列以保持向后兼容

这可以防止来自其他 chat、DM 对端或路由 agent 会话的消息被注入到错误的对话中。

## 配置

在 `config.json` 的 `agents.defaults` 下：

```json
{
  "agents": {
    "defaults": {
      "steering_mode": "one-at-a-time"
    }
  }
}
```

### 模式

| 值 | 行为 |
|-------|----------|
| `"one-at-a-time"` | **（默认）** 每个轮询周期只出队一条消息。如果队列中有 3 条消息，它们会在连续 3 次迭代中一次处理一条。 |
| `"all"` | 在单次轮询中清空整个队列。所有待处理消息会一起注入上下文。 |

也可以使用环境变量 `GOGOCLAW_AGENTS_DEFAULTS_STEERING_MODE`。

## Go API

### Steer — 发送 steering 消息

```go
err := agentLoop.Steer(providers.Message{
    Role:    "user",
    Content: "change direction, focus on X instead",
})
if err != nil {
    // Queue is full (MaxQueueSize=10) or not initialized
}
```

消息会以线程安全方式入队。如果队列已满或未初始化，则返回错误。它会在下一个轮询点（当前工具完成后）被取走。

### SteeringMode / SetSteeringMode

```go
// Read the current mode
mode := agentLoop.SteeringMode() // SteeringOneAtATime | SteeringAll

// Change it at runtime
agentLoop.SetSteeringMode(agent.SteeringAll)
```

### Continue — 恢复空闲 agent

当 agent 空闲时（它已完成处理，且最后一条消息来自 assistant），`Continue` 会检查队列中是否有 steering 消息，并使用它们开始新周期：

```go
response, err := agentLoop.Continue(ctx, sessionKey, channel, chatID)
if err != nil {
    // Error (e.g. "no default agent available")
}
if response == "" {
    // No steering messages in queue, the agent stays idle
}
```

`Continue` 内部使用 `SkipInitialSteeringPoll: true`，以避免同一批消息被重复出队（因为它已经提取了这些消息并直接作为输入传入）。

`Continue` 还会根据提供的会话键解析目标 agent，因此
agent 作用域会话会在正确的 agent 上继续，而不是始终使用默认 agent。

## Loop 中的轮询点

Steering 会在 agent 周期的以下位置检查：

1. **Loop 开始时** —— 在第一次 LLM 调用之前，捕获设置期间入队的消息
2. **每个工具完成后** —— 包括第一个和最后一个。如果发现 steering 且还有剩余工具，它们都会立即跳过
3. **直接 LLM 响应之后** —— 如果模型生成非工具响应期间有新 steering 消息到达，loop 会继续，而不是返回过时答案
4. **turn 即将 finalize 之前** —— 如果 steering 在 turn 的最后时刻到达，agent 会立即开始 continuation turn，而不是让消息孤立地留在队列中

## 为什么跳过剩余工具

当检测到 steering 消息时，批次中所有剩余工具都会被跳过，而不是执行。曾考虑并拒绝另一种方案——让所有工具完成后再注入 steering 消息。原因如下。

### 防止不希望的副作用

工具可能有**不可逆的副作用**。如果用户在 agent 批次执行中途说“不，等等”，执行剩余工具意味着这些副作用仍然会发生：

| 工具批次 | Steering 消息 | 跳过时 | 不跳过时 |
|---|---|---|---|
| `[web_search, send_email]` | "don't send it" | 邮件**未**发送 | 邮件已发送，损害已造成 |
| `[query_db, write_file, spawn_agent]` | "use another database" | 只运行查询 | 文件已写入 + 子代理已生成，全部浪费 |
| `[search₁, search₂, search₃, write_file]` | 用户完全改变主题 | 1 次搜索 | 3 次搜索 + 文件写入，全部无关 |

### 避免浪费时间

需要数秒的工具（网络抓取、API 调用、数据库查询）都会运行完成后 agent 才能看到用户的纠正。在一个 3 工具批次中，如果每个工具耗时 3-4 秒，那就是 10 多秒随后会被丢弃的工作。

通过跳过，agent 会在当前工具一完成时立即反应——通常只需几秒，而不是等待整个批次。

### LLM 获得完整上下文

被跳过的工具会收到明确的错误结果（`"Skipped due to queued user message."`），因此模型确切知道哪些操作没有执行。然后它可以决定是否使用新上下文重新执行这些操作，或完全采取不同路径。

### 权衡：串行执行

跳过要求工具**串行**运行（之前的实现并行运行它们）。当 LLM 在单个 turn 中请求多个独立工具时，这会引入延迟。在实践中，大多数批次包含 1-2 个工具，因此与能够停止不想要操作的收益相比，影响很小。

## 被跳过工具的结果格式

当 steering 中断一个批次时，每个未执行的工具都会收到一个 `tool` 结果，其中包含：

```
Content: "Skipped due to queued user message."
```

这会通过 `AddFullMessage` 保存到会话并发送给模型，因此它知道某些请求的操作没有执行。

## 完整流程示例

```
1. User: "search for info on X, write a file, and send me a message"

2. LLM responds with 3 tool calls: [web_search, write_file, message]

3. web_search is executed → result saved

4. [polling] → User called Steer("no, search for Y instead")

5. write_file is skipped → "Skipped due to queued user message."
   message is skipped    → "Skipped due to queued user message."

6. Message "search for Y instead" injected into context

7. LLM receives the full updated context and responds accordingly
```

## 自动 bus drain

当 agent loop（`Run()`）启动时，它从共享 message bus 读取入站消息。路由逻辑决定每条消息如何处理：

1. **消息所属会话没有活动 turn** —— 消息被分发到一个 **worker goroutine**，由它处理完整 turn（LLM 调用、工具执行、steering drain）
2. **同一会话已存在活动 turn** —— 消息通过 `enqueueSteeringMessage` 直接进入该会话的 **steering 队列**。不需要后台 drain goroutine
3. **不可路由消息**（例如 `system`）—— 在主循环中同步处理

此设计支持**来自不同会话的消息并行处理**，同时让同一会话的消息严格串行。关键影响：

- 来自不同用户/渠道的消息会**并发**处理（最多 `max_parallel_turns`）
- 来自同一会话的消息会**串行化** —— 后续消息进入 steering 队列
- 用户不需要做任何特殊操作 —— 当 agent 正在忙于其会话时，他们的消息会自动作为 steering 捕获
- 音频消息会在处理该 turn 的 worker 内转录，因此 agent 收到的是文本
- `system` 入站消息会立即处理，不会触发 steering

## 带 media 的 steering

Steering 消息可以包含 `Media` 引用，就像普通入站用户消息一样。

- 原始 `media://` 引用会通过 `AddFullMessage` 保留在会话历史中
- 下一次 Provider 调用前，steering 消息会经过正常的 media 解析流水线
- 图像引用会转换为 data URL 供多模态 Provider 使用；非图像引用按标准入站 media 的方式解析

这同时适用于 turn 内 steering 和通过
`Continue()` 进行的空闲会话继续。

## 注意事项

- Steering **不会中断**当前正在执行的工具。它会等待当前工具完成，然后检查队列。
- 在 `one-at-a-time` 模式下，如果多条消息快速入队，它们会每次迭代处理一条。这给模型分别响应每条消息的机会。
- 在 `all` 模式下，所有待处理消息会合并为一次注入。当你希望 agent 一次性接收所有上下文时很有用。
- Steering 队列最大容量为 10 条消息（`MaxQueueSize`）。队列已满时 `Steer()` 返回错误。在 bus drain 路径中，错误会作为警告记录，消息实际上会被丢弃。
- 在活动 turn 之外手动调用 `Steer()` 仍会进入旧版回退队列，因此旧集成可以继续工作。
