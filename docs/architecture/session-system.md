# Session System

> Back to [README](../README.md)

This document describes the runtime session system used by GoGoClaw to:

- map inbound messages onto stable conversation scopes
- persist message history and summaries
- preserve compatibility with legacy `agent:...` session keys while the runtime uses opaque canonical keys

This document covers the core runtime path in `pkg/session`, `pkg/memory`, and `pkg/agent`.
It does not describe launcher login cookies or dashboard authentication sessions in `web/backend/middleware`.

## Responsibilities

The session system has four jobs:

1. Decide which messages should share the same conversation context.
2. Persist that context durably across turns and restarts.
3. Expose a small `SessionStore` interface to the agent loop.
4. Keep older session-key formats readable without using them for new writes.

## Main Components

| Layer | Files | Responsibility |
| --- | --- | --- |
| Session contract | `pkg/session/session_store.go` | Defines the `SessionStore` interface used by the agent loop. |
| Legacy fallback | `pkg/session/manager.go` | In-memory/file manager used only if the SQLite store cannot initialize. |
| Session adapter | `pkg/session/store_backend.go` | Adapts `pkg/memory.Store` to `SessionStore`, including alias and scope metadata support. |
| Durable storage | `pkg/memory/sqlite.go` | Canonical `sessions.db` store for messages, summaries, aliases, and turn checkpoints. |
| Scope and key building | `pkg/session/scope.go`, `pkg/session/key.go`, `pkg/session/allocator.go` | Builds structured scopes, opaque canonical keys, and legacy aliases from routing results. |
| Runtime integration | `pkg/agent/instance.go`, `pkg/agent/agent.go`, `pkg/agent/agent_message.go` | Initializes the store, allocates session scope, and persists metadata before turns run. |

## Session Data Model

The structured session identity is represented by `session.SessionScope`:

| Field | Meaning |
| --- | --- |
| `Version` | Schema version. Current value is `ScopeVersionV1`. |
| `AgentID` | Routed agent handling the turn. |
| `Channel` | Normalized inbound channel name. |
| `Account` | Normalized account or bot identifier. |
| `Dimensions` | Ordered list of active partition dimensions such as `chat` or `sender`. |
| `Values` | Concrete normalized values for each selected dimension. |

Only four dimensions are currently recognized by the allocator:

- `space`
- `chat`
- `topic`
- `sender`

The default config uses:

```json
{
  "session": {
    "dimensions": ["chat"]
  }
}
```

That means one shared conversation per chat unless a dispatch rule overrides it.

## Canonical Keys And Legacy Aliases

The runtime now prefers opaque canonical keys:

```text
sk_v1_<sha256>
```

These keys are built from a canonical scope signature in `pkg/session/key.go`.
The goal is to make storage keys stable while decoupling them from any specific legacy text format.

For compatibility, the allocator also emits legacy aliases such as:

```text
agent:main:direct:user123
agent:main:slack:channel:c001
agent:main:pico:direct:pico:session-123
```

These aliases matter because older sessions, tests, and some tools still refer to the legacy shape.
The storage backend resolves aliases back to the canonical key before reads and writes.

The agent loop also preserves explicit incoming session keys when the caller already supplied one of the recognized explicit formats:

- opaque canonical key
- legacy `agent:...` key

That behavior lives in `pkg/agent/agent_utils.go:resolveScopeKey`.

## Allocation Flow

The end-to-end flow for a normal inbound message is:

```text
InboundMessage
  -> RouteResolver.ResolveRoute(...)
  -> session.AllocateRouteSession(...)
  -> resolveScopeKey(...)
  -> ensureSessionMetadata(...)
  -> AgentLoop turn execution
  -> SessionStore read/write operations
```

More concretely:

1. `pkg/agent/agent_message.go` resolves the agent route from normalized inbound context.
2. `session.AllocateRouteSession` converts the route's `SessionPolicy` plus inbound context into a structured `SessionScope`.
3. The allocator builds:
   - `SessionKey`: canonical routed session key
   - `SessionAliases`: compatibility aliases for that routed scope
   - `MainSessionKey`: agent-level main session key
   - `MainAliases`: legacy alias for the main session
4. `runAgentLoop` persists scope metadata and aliases through `ensureSessionMetadata`.
5. During later reads or writes, `StoreBackend.ResolveSessionKey` maps aliases back onto the canonical key.

The main session key is separate from routed chat sessions.
It is mainly used for agent-level or system-style flows that need one stable per-agent conversation, for example `processSystemMessage`.

## Scope Construction Rules

`pkg/session/allocator.go` builds scope values from normalized inbound context.
Important rules:

- `space` becomes `<space_type>:<space_id>`
- `chat` becomes `<chat_type>:<chat_id>`
- `topic` becomes `topic:<topic_id>`
- `sender` is canonicalized through `session.identity_links` before being stored

There are two special cases worth calling out.

### Telegram forum isolation

Telegram forum topics must stay isolated even when the configured dimensions only mention `chat`.
To preserve that behavior, the allocator appends `/<topic_id>` to the `chat` value for Telegram forum messages unless `topic` is already an explicit dimension.

Example:

```text
group:-1001234567890/42
group:-1001234567890/99
```

Those produce different session keys.

### Identity links

`session.identity_links` lets multiple sender identifiers collapse into one canonical identity.
Both dispatch matching and session allocation use that mapping so that the same person can keep one conversation even if their raw sender IDs differ across channels or accounts.

## Storage Format

The canonical runtime store is `pkg/memory.SQLiteStore`, wrapped by
`session.StoreBackend`. It opens `sessions/sessions.db` in WAL mode with a
busy timeout and foreign keys enabled.

The database contains three session tables:

| Table | Contents |
| --- | --- |
| `session_meta` | summary, logical skip offset, timestamps, scope, turn checkpoint and turn history |
| `session_messages` | stable message id, role, searchable text, and the complete `providers.Message` payload |
| `session_aliases` | legacy alias to canonical session-key mapping |

Messages are ordered by a database id. UI pagination uses that id as an opaque
cursor, so edits do not depend on JSONL line numbers. `session_meta.skip_count`
marks the start of the active model context while the complete transcript stays
available for the Web history, exports, branches, attachments, and search.

`sessions/seahorse.db` remains a derived retrieval and summary index. It can be
rebuilt from the canonical session transcript; it is not the source of truth for
chat history.

Legacy `.json` and `.jsonl` files are read-only compatibility input for the Web
console. New writes always go to `sessions.db`; there is no automatic migration.

## Write And Crash Semantics

Each session mutation runs in a SQLite transaction:

- appending a message inserts it and advances the session timestamp atomically;
- `SetHistory` replaces messages and resets the logical skip offset in one transaction;
- `CommitSummary` verifies the summary and message prefix before advancing the offset;
- turn checkpoints and delivery state use the same transactional metadata row.

`TruncateHistory` is logical: it only changes `skip_count`. `Compact` is a no-op
for SQLite. Deleting a session cascades to its messages and aliases; the freed
pages remain on SQLite's freelist and are reused by later inserts. GoGoClaw does
not run `VACUUM` on deletion because repeated whole-database rewrites create
avoidable disk I/O.

## Concurrency Model

SQLite WAL mode allows concurrent readers and one writer. The store uses a
bounded connection pool, closes idle handles between operations, and relies on a
five-second busy timeout for short write contention. Every multi-row mutation is
transactional, so the agent loop does not need storage-specific locking.

The legacy `SessionManager` uses a single in-memory map guarded by an RW mutex.
It satisfies the same `SessionStore` contract only as an initialization fallback.

## Compatibility And Startup

`pkg/agent/instance.go:initSessionStore` opens the canonical SQLite store and
wraps it with `session.NewStoreBackend`. If the database cannot initialize, the
agent logs the error and falls back to `SessionManager` for that run.

### Alias promotion

When canonical metadata is first created, `EnsureSessionMetadata` may promote history from a non-empty legacy alias into the canonical session.
That promotion only happens when the canonical session is still empty, so active canonical history is not overwritten.

This is how the system preserves old histories such as:

- legacy direct-message keys
- older Pico direct-session keys

while moving the runtime onto opaque canonical keys.

## Other SessionStore Implementations

`pkg/agent/subturn.go` defines an `ephemeralSessionStore`.
It satisfies the same `SessionStore` interface, but keeps data in memory only and is destroyed when the sub-turn ends.

That lets SubTurn reuse the same session-facing APIs without writing child-session history into the parent's durable storage.

## Operational Consumers

The session system is consumed by more than the agent loop:

- `web/backend/api/session.go` reads `sessions.db` and retains read-only JSONL/JSON compatibility for the launcher UI.
- `pkg/agent/steering.go` can recover scope metadata for active steering flows.
- tooling and tests can still refer to legacy aliases because alias resolution is handled below the agent loop.

## Related Files

- `pkg/session/session_store.go`
- `pkg/session/manager.go`
- `pkg/session/store_backend.go`
- `pkg/session/scope.go`
- `pkg/session/key.go`
- `pkg/session/allocator.go`
- `pkg/memory/sqlite.go`
- `pkg/agent/instance.go`
- `pkg/agent/agent.go`
- `pkg/agent/agent_message.go`
