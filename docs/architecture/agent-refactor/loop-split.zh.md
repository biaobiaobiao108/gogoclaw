# AgentLoop 文件拆分

> **注意：** 本文描述的是上一阶段完成的文件拆分。`loop_*` 命名此后已重命名为 `agent_*` 和 `turn_*`。当前文件结构请参见 [agent-rename-plan.md](./agent-rename-plan.md)。

## 概述

`pkg/agent/loop.go` 文件（最初为 4384 行）已拆分为 12 个职责明确的源文件。这是一次纯重构，没有行为变更。

## 目标

- 浏览 agent loop 代码时降低认知负担
- 通过解耦关注点支持并行工作
- 保持所有现有功能与测试
- 让每个文件的 import 保持最少

## 原始文件映射（阶段 2 中重命名）

| 旧文件 | 新文件 | 职责 |
|----------|----------|----------------|
| `loop.go` | `agent.go` | 核心 `AgentLoop` 结构体、`Run`、`Stop`、`Close` |
| `loop_turn.go` | `turn_coord.go` + `pipeline_*.go` | Turn 执行：coordinator + Pipeline 方法 |
| `loop_utils.go` | `agent_utils.go` | 独立工具函数 |
| `loop_init.go` | `agent_init.go` | `NewAgentLoop` 构造函数和工具注册 |
| `loop_message.go` | `agent_message.go` | 消息处理与路由 |
| `loop_command.go` | `agent_command.go` | 命令处理 |
| `loop_mcp.go` | `agent_mcp.go` | MCP 运行时 |
| `loop_event.go` | `agent_event.go` | 事件系统辅助函数 |
| `loop_media.go` | `agent_media.go` | Media 解析 |
| `loop_outbound.go` | `agent_outbound.go` | 响应发布 |
| `loop_transcribe.go` | `agent_transcribe.go` | 音频转录 |
| `loop_steering.go` | `agent_steering.go` | Steering 队列 |
| `loop_inject.go` | `agent_inject.go` | Setter 注入 |

## 当前文件结构

完整当前文件结构请参见 [agent-rename-plan.md](./agent-rename-plan.md)。

## 阶段 2：重命名与 Pipeline 重构

阶段 2 完成了以下工作：

1. **文件重命名**：所有 `loop_*` 文件重命名为 `agent_*` 或 `turn_*`
2. **Turn 状态合并**：`turn.go` + `turn_exec.go` → `turn_state.go`
3. **Pipeline 提取**：将大型 `runTurn` 拆分为 Pipeline 方法

### Pipeline 架构

Pipeline 方法提供结构化的 turn 执行：

| 方法 | 文件 | 职责 |
|--------|------|----------------|
| `SetupTurn()` | `pipeline_setup.go` | 历史组装、消息构建、候选选择 |
| `CallLLM()` | `pipeline_llm.go` | PreLLM hooks、fallback、retry、AfterLLM hooks |
| `ExecuteTools()` | `pipeline_execute.go` | 带 hooks 的工具执行 |
| `Finalize()` | `pipeline_finalize.go` | 会话持久化、压缩 |

## 应用的核心原则

### 1. 同一包，独立文件
所有文件都属于 `agent` 包并一起编译。这保留了原有的可见性规则。

### 2. 不改变逻辑
所有函数都按原样移动。提取过程保持了行为等价。

### 3. 共享类型放在 turn_state.go
`turnState`、`turnExecution`、`Control`、`ToolControl` 和 `LLMPhase` 类型集中在 `turn_state.go`。

## 测试

所有现有测试通过。5 个失败测试（`TestGlobalSkillFileContentChange` 和 4 个 Seahorse 测试）是既有失败，与本次重构无关。

构建状态：`go build ./pkg/agent/...` 通过，无错误。

## 另见

- [agent-rename-plan.md](./agent-rename-plan.md) — 当前文件命名约定
- [context.md](context.md) — 上下文管理与会话处理
