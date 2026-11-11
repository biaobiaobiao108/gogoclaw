# Turn 生命周期

每个 agent turn 都遵循一个显式状态机。`pkg/agent/turn_coord.go` 中的
coordinator 拥有所有阶段转换；pipeline 阶段执行工作，但不会让 turn
在阶段之间移动。

```text
created -> setup -> running <-> tools -> finalizing -> completed
                  |          |             |
                  +----------+-------------+-> failed
                  +----------+-------------+-> aborted
```

## 规则

- 重复转换到当前阶段是空操作；每条真实边都会记录在有界历史中供诊断使用。
- `completed` 只能通过 `finalizing` 到达。
- `failed` 和 `aborted` 是终态，可以从活动阶段进入。
- 一个 turn 只在一个终态阶段结束，由 coordinator 的延迟 finish 路径设置。panic 会在 cleanup 运行前记录为 failed。
- 终态阶段永远不会转换回活动阶段。

## 异步投递

异步 sub-turn 和工具结果没有单独的轮询路径。生产者将一个 `asyncDelivery` 推入所属 turn 的队列。coordinator 在下一次模型调用前消费一次该队列。如果 turn 已停止或队列已满，结果会被暂存，并在旧 turn 注销后作为 follow-up 入站 turn 发布。

## 评测 Harness

`pkg/harness` 针对真实 `AgentLoop` 运行脚本化场景。它记录 Provider 调用、已执行工具顺序、运行时事件、最终历史和出站消息，然后评估声明式轨迹期望。它支持多 turn 会话、异步投递和 follow-up turn、Provider/工具/会话故障、canonical golden trace、Provider 录制/回放以及结构化 suite 报告。参见 [评测 Harness](harness.md)。
