# 架构

主要运行时机制与子系统设计的内部架构说明。

- [Steering](steering.md)：在工具调用之间向运行中的 agent loop 注入消息。
- [Turn 生命周期](turn-lifecycle.md)：显式阶段转换与统一的异步结果投递。
- [评测 Harness](harness.md)：轨迹断言、故障注入、trace 回放、产品场景和模型行为评测。
- [SubTurn 机制](subturn.md)：子代理协调、并发控制和生命周期处理。
- [Session 系统](session-system.md)：会话作用域分配、canonical SQLite 持久化、alias 兼容与旧数据读取。（[中文](session-system.zh.md)）
- [路由系统](routing-system.md)：agent 分发、会话策略选择和轻量/重量模型路由。（[中文](routing-system.zh.md)）
- [运行时事件](runtime-events.md)：运行时事件 envelope、集中式事件日志、过滤器和示例。（[中文](runtime-events.zh.md)）
- [Agent 自我演化](agent-self-evolution.md)：学习记录、草稿生成、应用模式与状态布局。
- [Hook 系统指南](hooks/README.md)：当前 hook 架构与协议细节。
- [Agent 重构](agent-refactor/README.md)：agent 重构工作的说明与检查点。

提案式或探索性文档另见 [`../design/`](../design/)。
