# Agent 自我演化

Agent 自我演化让 GoGoClaw 从已完成的 turn 中学习，并将重复出现的成功行为转化为技能改进。运行时由顶层 `evolution` 配置块控制。

## 流程

热路径在 agent turn 结束时运行。当 `evolution.enabled` 为 true 时，它会记录一条学习记录，包含 turn 摘要、成功状态、使用的 skills、工具执行以及 session/workspace 元数据。Heartbeat turn 会跳过。

冷路径会对相关任务记录分组，检查配置的成功阈值，并为证据足够的模式准备技能草稿。草稿可以针对新技能，也可以追加/替换/合并现有工作区技能。

应用路径会在写入前校验生成的 `SKILL.md` 内容。无效草稿会在创建技能目录或文件之前被拒绝。

## 安全考虑

演化会创建一个持久反馈循环：用户输入可以成为任务记录，任务记录可以聚类成 LLM 生成的草稿，而接受的草稿可以成为加载到未来 agent prompt 中的 `SKILL.md` 内容。尤其在使用 `apply` 模式时，应将生成的技能内容视为对 prompt 敏感的材料。

当前本地扫描器只是一个狭窄的护栏，不是完整的安全边界。它会拒绝结构无效的草稿和少量明显的类密钥子串，但不能可靠检测 prompt injection、不安全指令或每一种敏感数据形式。当技能变更写入磁盘前需要人工审查时，请使用 `observe` 或 `draft`。

在 `apply` 模式下，接受的草稿可以自动更新工作区技能。现有技能在替换前会备份，但恢复是手动的：如果已应用的技能需要回滚，运维人员必须恢复所需的备份。

## 模式

| 模式 | 行为 |
|------|----------|
| `observe` | 只记录学习数据。不会自动运行冷路径草稿生成。 |
| `draft` | 记录学习数据，并在冷路径运行时生成候选技能草稿。 |
| `apply` | 生成草稿，并允许接受的草稿更新工作区技能。 |

当 `evolution.enabled` 为 false 时，运行时会将 `mode` 视为禁用。

## 冷路径触发

`cold_path_trigger` 仅在 `draft` 和 `apply` 模式下有意义。

| 触发方式 | 行为 |
|---------|----------|
| `after_turn` | 在符合条件的 turn 后运行冷路径。 |
| `scheduled` | 在配置的 `cold_path_times` 运行冷路径。 |
| `manual` | 不自动运行。目前没有面向用户的 Web/API/CLI 触发器；代码仍然可以调用 `Runtime.RunColdPathOnce`。 |

`cold_path_times` 使用 `HH:MM` 字符串，除非触发器为 `scheduled`，否则会被忽略。

## 状态

默认情况下，演化状态存储在工作区下。`state_dir` 可以将该状态重定向到另一个目录。状态包括学习记录、聚类模式记录、草稿和技能 profiles。

用户可见的配置字段请参见 [配置指南](../guides/configuration.md#agent-self-evolution)。
