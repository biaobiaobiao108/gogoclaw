# 上下文

## 本文涵盖的内容

本文明确 agent loop 中上下文管理的边界：

- 什么填充上下文窗口，以及空间如何划分
- 什么存储在会话历史中，什么在请求时构建
- 上下文压缩何时发生以及如何发生
- token 预算如何估算

这些都是现有概念。本文澄清它们的边界，而不是引入新概念。

---

## 上下文窗口区域

上下文窗口是模型的总输入容量。四个区域填充它：

| 区域 | 由谁组装 | 是否存储在会话中？ |
|---|---|---|
| System prompt | `BuildMessages()` — static + dynamic parts | 否 |
| 摘要 | `SetSummary()` 存储它；`BuildMessages()` 注入它 | 与历史分开 |
| 会话历史 | User / assistant / tool 消息 | 是 |
| 工具定义 | Provider 适配器在调用时注入 | 否 |

`MaxTokens`（输出生成限制）也必须从总预算中预留。

因此历史可用空间为：

```
history_budget = ContextWindow - system_prompt - summary - tool_definitions - MaxTokens
```

---

## ContextWindow 与 MaxTokens

二者用途不同：

- **MaxTokens** — LLM 在一次响应中最多可生成的 token 数。作为 `max_tokens` 请求参数发送。
- **ContextWindow** — 模型的总输入上下文容量。

它们以前被设置为相同的值，这导致摘要阈值要么触发得过早（默认 32K 时），要么完全不触发（当用户提高 `max_tokens` 时）。

当前未显式配置时的默认值：`ContextWindow = MaxTokens * 4`。

---

## 会话历史

会话历史只存储对话消息：

- `user` — 用户输入
- `assistant` — LLM 响应（可能包含 `ToolCalls`）
- `tool` — 工具执行结果

会话历史**不**包含：

- System prompt — 由 `BuildMessages` 在请求时组装
- 摘要内容 — 通过 `SetSummary` 单独存储，由 `BuildMessages` 注入

这一区别很重要：任何操作会话历史的代码——压缩、边界检测、token 估算——都不能假设存在 system 消息。

---

## Turn

一个 **Turn** 是一个完整周期：

> user message -> LLM iterations (possibly including tool calls) -> final assistant response

这个定义来自 agent loop 设计（#1316）。在会话历史中，Turn 边界通过 `user` 角色的消息识别。

Turn 是压缩的原子单位。在 Turn 内部切分会遗弃工具调用序列——一条带有 `ToolCalls` 的 assistant 消息与其对应的 `tool` 结果被分开。在 Turn 边界压缩可以从构造上避免这一点。

`parseTurnBoundaries(history)` 返回每个 Turn 的起始索引。
`findSafeBoundary(history, targetIndex)` 将目标切点吸附到最近的 Turn 边界。

---

## 压缩路径

存在三条压缩路径，按优先级排序：

### 1. 异步摘要

`maybeSummarize` 在每个 Turn 完成后运行。

当消息数量超过阈值，或估算的历史 token 超过 `ContextWindow` 的某个百分比时触发。如果触发，后台 goroutine 会调用 LLM，为最旧的消息生成摘要。摘要通过 `SetSummary` 存储；`BuildMessages` 在下次调用时将其注入 system prompt。

切点使用 `findSafeBoundary`，因此不会拆分 Turn。

### 2. 主动预算检查

`isOverContextBudget` 在每次 LLM 调用前运行。

使用完整预算公式：`message_tokens + tool_def_tokens + MaxTokens > ContextWindow`。如果超出预算，就触发 `forceCompression`，并在调用 LLM 前重建消息。

这可以防止原本会因上下文窗口错误而失败的无用（且会计费）LLM 调用。

### 3. 紧急压缩（响应式）

`forceCompression` 在 LLM 尽管经过主动检查仍返回上下文窗口错误时运行。

丢弃最旧的约 50% Turn。如果历史只有一个 Turn 且没有安全切分点（例如一条用户消息后跟着一个巨大的工具响应），则回退为只保留最近的用户消息——作为最后手段破坏 Turn 原子性，以避免上下文超限循环。

将压缩说明存储在会话摘要中（而不是历史消息中），以便 `BuildMessages` 能将其包含在下一个 system prompt 中。

这是 token 估算低于现实时的回退方案。

---

## Token 估算

估算使用约每 token 2.5 个字符的启发式规则（`chars * 2 / 5`）。

`estimateMessageTokens` 统计：

- `Content`（rune 数量，以保证多字节正确性）
- `ReasoningContent`（扩展思考 / chain-of-thought）
- `ToolCalls` — ID、类型、函数名、参数
- `ToolCallID`（工具结果元数据）
- 每条消息的开销（角色标签、JSON 结构）
- `Media` 项 — 按每项固定 token 估算，直接加到最终计数（不经过字符启发式，因为实际成本取决于分辨率和 Provider 特定的图像 tokenization）

`estimateToolDefsTokens` 统计工具定义开销：名称、说明、参数的 JSON schema。

这些估算有意采用启发式。主动检查处理常见情况；响应式路径捕获估算错误。

---

## 接口边界

上下文预算函数（`parseTurnBoundaries`、`findSafeBoundary`、`estimateMessageTokens`、`isOverContextBudget`）是**纯函数**。它们接收 `[]providers.Message` 和整数参数。它们不依赖 `AgentLoop` 或任何其他运行时结构体。

`BuildMessages` 是最终发送给 LLM 的消息数组的唯一组装者。预算函数为压缩决策提供信息，但不构造消息。

`forceCompression` 和 `summarizeSession` 修改会话状态（历史与摘要）。`BuildMessages` 读取该状态以构造上下文。流程为：

```
budget check --> compression decision --> mutate session --> BuildMessages reads session --> LLM call
```

---

## 已知缺口

以下是当前实现中已确认的限制，记录于此以便可见：

- **摘要触发没有使用完整预算公式。** `maybeSummarize` 将估算的历史 token 与 `ContextWindow` 的百分比进行比较。它没有考虑 system prompt 大小、工具定义开销或 `MaxTokens` 预留。主动检查覆盖了关键路径（防止 400 错误），但摘要触发可以与同一预算模型对齐，以实现更准确的早期压缩。

- **Token 估算是启发式的。** 它没有考虑 Provider 特定 tokenization、准确的 system prompt 大小（单独组装）或可变的图像 token 成本。双路径设计（主动 + 响应式）旨在容忍这种不精确性。

- **响应式重试不保留 media。** 当响应式路径在压缩后重建上下文时，它目前为 media 引用传入空值。这是主循环中已存在的问题，不是预算系统引入的。

---

## 本文不涵盖的内容

- `AGENT.md` frontmatter 如何配置上下文参数——这是 Agent 定义工作的一部分
- 上下文构建器在新架构中如何组装上下文——这是即将开展的工作
- 压缩事件如何通过事件系统暴露——这是事件模型（#1316）的一部分
- 子代理上下文隔离——这是独立轨道
