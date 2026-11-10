# Session 系统

> 返回 [README](../README.md)

本文说明 GoGoClaw 运行时的 Session 系统如何完成以下事情：

- 把入站消息映射到稳定的会话作用域
- 持久化消息历史与摘要
- 在运行时使用不透明 canonical key 的同时，继续兼容旧的 `agent:...` session key

本文覆盖 `pkg/session`、`pkg/memory` 和 `pkg/agent` 中的核心运行时链路。
它不讨论 `web/backend/middleware` 中 launcher 登录 Cookie 或 dashboard 鉴权 session。

## 职责

Session 系统承担四件事：

1. 决定哪些消息应该共享同一段上下文。
2. 让这段上下文能跨 turn、跨进程重启持久存在。
3. 向 agent loop 暴露一个足够小的 `SessionStore` 抽象。
4. 让旧 session key 仍可读取，但不再用于新的持久化写入。

## 主要组件

| 层次 | 文件 | 作用 |
| --- | --- | --- |
| Session 抽象 | `pkg/session/session_store.go` | 定义 agent loop 依赖的 `SessionStore` 接口。 |
| 旧回退 | `pkg/session/manager.go` | 仅当 SQLite 无法初始化时使用的内存/文件回退实现。 |
| Session 适配层 | `pkg/session/store_backend.go` | 把 `pkg/memory.Store` 适配成 `SessionStore`，并支持 alias 与 scope metadata。 |
| 持久化存储 | `pkg/memory/sqlite.go` | 用 `sessions.db` 统一保存消息、摘要、alias 和 turn checkpoint。 |
| Scope / Key 构建 | `pkg/session/scope.go`、`pkg/session/key.go`、`pkg/session/allocator.go` | 从路由结果生成结构化 scope、不透明 canonical key 和 legacy alias。 |
| 运行时集成 | `pkg/agent/instance.go`、`pkg/agent/loop.go`、`pkg/agent/loop_message.go` | 初始化存储、分配 session scope，并在 turn 执行前落 metadata。 |

## Session 数据模型

结构化的会话身份由 `session.SessionScope` 表示：

| 字段 | 含义 |
| --- | --- |
| `Version` | Scope 模式版本，当前为 `ScopeVersionV1`。 |
| `AgentID` | 处理该 turn 的路由 agent。 |
| `Channel` | 归一化后的入站 channel 名称。 |
| `Account` | 归一化后的 bot / account 标识。 |
| `Dimensions` | 当前启用的隔离维度顺序，例如 `chat` 或 `sender`。 |
| `Values` | 每个维度对应的具体归一化值。 |

Allocator 当前只识别四个维度：

- `space`
- `chat`
- `topic`
- `sender`

默认配置是：

```json
{
  "session": {
    "dimensions": ["chat"]
  }
}
```

也就是默认按 chat 共享上下文；如果 dispatch rule 覆盖了维度，则以 rule 为准。

## Canonical Key 与 Legacy Alias

运行时现在优先使用不透明 canonical key：

```text
sk_v1_<sha256>
```

它由 `pkg/session/key.go` 中的 scope signature 计算得到。
这样可以让存储 key 稳定，同时不再把持久化格式和某一种旧文本 key 绑定死。

为了兼容旧数据，allocator 还会生成 legacy alias，例如：

```text
agent:main:direct:user123
agent:main:slack:channel:c001
agent:main:pico:direct:pico:session-123
```

这些 alias 很重要，因为旧 session、部分测试以及某些工具仍然会引用这种格式。
存储 backend 会在读写前先把 alias 解析回 canonical key。

此外，如果调用方已经显式传入了受支持的 session key，agent loop 会保留它，不强行改成新分配的 routed key。
这条逻辑在 `pkg/agent/loop_utils.go:resolveScopeKey` 中：

- 不透明 canonical key
- legacy `agent:...` key

都属于“显式 key”。

## 分配流程

普通入站消息的完整链路如下：

```text
InboundMessage
  -> RouteResolver.ResolveRoute(...)
  -> session.AllocateRouteSession(...)
  -> resolveScopeKey(...)
  -> ensureSessionMetadata(...)
  -> AgentLoop turn 执行
  -> SessionStore 读写
```

具体来说：

1. `pkg/agent/loop_message.go` 先用归一化后的 inbound context 解析 agent route。
2. `session.AllocateRouteSession` 把 route 的 `SessionPolicy` 和 inbound context 组合成结构化 `SessionScope`。
3. Allocator 会生成：
   - `SessionKey`：当前路由会话的 canonical key
   - `SessionAliases`：该路由会话的兼容 alias
   - `MainSessionKey`：agent 级主会话 key
   - `MainAliases`：主会话对应的 legacy alias
4. `runAgentLoop` 通过 `ensureSessionMetadata` 持久化 scope metadata 和 alias。
5. 后续读写时，`StoreBackend.ResolveSessionKey` 会先把 alias 映射回 canonical key。

`MainSessionKey` 和普通聊天会话是分开的。
它主要服务于 agent 级、系统级的上下文场景，比如 `processSystemMessage`。

## Scope 构建规则

`pkg/session/allocator.go` 会从归一化后的 inbound context 生成 scope 值。
关键规则如下：

- `space` 变成 `<space_type>:<space_id>`
- `chat` 变成 `<chat_type>:<chat_id>`
- `topic` 变成 `topic:<topic_id>`
- `sender` 会先经过 `session.identity_links` 归一化再写入

其中有两个需要单独记住的特殊规则。

### Telegram forum 隔离

Telegram forum topic 必须默认保持隔离，即使配置只写了 `chat` 维度。
为此，如果消息来自 Telegram forum 且策略里没有显式包含 `topic`，allocator 会把 `/<topic_id>` 拼到 `chat` 值后面。

例如：

```text
group:-1001234567890/42
group:-1001234567890/99
```

这两者会得到不同的 session key。

### Identity links

`session.identity_links` 可以把多个 sender 标识折叠为一个 canonical identity。
dispatch 匹配和 session 分配都会使用这套映射，因此同一个人即使跨 channel 或 account 使用不同原始 sender ID，也可以继续落到同一段上下文里。

## 存储格式

运行时唯一事实库是 `pkg/memory.SQLiteStore`，由 `session.StoreBackend` 包装。
它使用 WAL、busy timeout 和外键打开 `sessions/sessions.db`。

库内有三张会话表：

| 表 | 内容 |
| --- | --- |
| `session_meta` | 摘要、逻辑截断偏移、时间戳、scope、turn checkpoint 与历史 |
| `session_messages` | 稳定消息 ID、角色、可搜索文本和完整 `providers.Message` 载荷 |
| `session_aliases` | legacy alias 到 canonical session key 的映射 |

消息按数据库 ID 排序，Web 分页把这个 ID 当作不透明游标，不再依赖 JSONL 行号。
`session_meta.skip_count` 只标记模型活动上下文的起点，完整 transcript 仍供 Web
历史、导出、分支、附件和搜索使用。

`sessions/seahorse.db` 保留为可重建的检索与摘要索引，不再是聊天记录的事实来源。

旧 `.json` / `.jsonl` 文件只作为 Web 控制台的只读兼容输入；新写入全部进入
`sessions.db`，启动时不会自动迁移旧数据。

## 写入与崩溃语义

每次会话变更都在 SQLite 事务内完成：

- 追加消息与更新时间戳原子提交；
- `SetHistory` 在同一事务里替换消息并把逻辑偏移归零；
- `CommitSummary` 先校验摘要和消息前缀，再推进偏移；
- turn checkpoint 与 delivery 状态写入同一行元数据。

`TruncateHistory` 只推进 `skip_count`。SQLite 后端的 `Compact` 是空操作。
删除会话会级联删除消息和 alias；释放的页留在 SQLite freelist 中供后续写入复用。
GoGoClaw 不会在删除时执行 `VACUUM`，避免反复重写整个数据库造成不必要的磁盘压力。

## 并发模型

SQLite WAL 允许多个读者和一个写者。存储层使用有界连接池、在操作之间关闭空闲句柄，
并用 5 秒 busy timeout 处理短暂写竞争。所有多行变更都在事务中完成，agent loop
不需要存储后端特化锁。

旧 `SessionManager` 只是一个内存 map 加 RW mutex，仅作为初始化失败时的回退。

## 兼容与启动

`pkg/agent/instance.go:initSessionStore` 打开唯一 SQLite 会话库，并用
`session.NewStoreBackend` 包装。只有数据库无法初始化时，才会记录错误并在该轮
运行中回退到 `SessionManager`。

### Alias 提升

第一次为 canonical key 建 metadata 时，`EnsureSessionMetadata` 会尝试把某个非空 legacy alias 的历史提升到 canonical session。
但这件事只会在 canonical session 仍然为空时发生，因此不会覆盖已经存在的 canonical 历史。

这保证了系统在迁移到 opaque key 的同时，仍能保留旧历史，例如：

- 旧的 direct-message key
- 旧的 Pico direct-session key

## 其他 SessionStore 实现

`pkg/agent/subturn.go` 里定义了 `ephemeralSessionStore`。
它同样实现 `SessionStore`，但只存在于内存里，在 sub-turn 结束时销毁。

这样 SubTurn 就能复用相同的 session 接口，而不会把子任务历史写进父会话的持久存储。

## 运行时消费者

Session 系统不只被 agent loop 使用：

- `web/backend/api/session.go` 读取 `sessions.db`，并保留 JSONL/JSON 旧格式的只读兼容。
- `pkg/agent/steering.go` 可以在 steering 场景下恢复 scope metadata。
- 因为 alias 解析发生在 agent loop 之下，测试和工具仍然可以继续使用 legacy alias。

## 相关文件

- `pkg/session/session_store.go`
- `pkg/session/manager.go`
- `pkg/session/store_backend.go`
- `pkg/session/scope.go`
- `pkg/session/key.go`
- `pkg/session/allocator.go`
- `pkg/memory/sqlite.go`
- `pkg/agent/instance.go`
- `pkg/agent/loop.go`
- `pkg/agent/loop_message.go`
