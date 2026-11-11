# GoGoClaw Hook System Design (Based on `refactor/agent`)

> Current status: This document is an early design record for the hook system. After the event system upgrade, the main path for observational hooks has moved to
> `pkg/events.Event`, `RuntimeEventObserver`, and the process hook's `hook.runtime_event`.
> The legacy `agent.Event`, `EventKind`, and `hook.event` compatibility layers have been removed.

## Background

This design centers on two topics:

- `#1316`: refactor the agent loop to be event-driven, interruptible, appendable, and observable
- `#1796`: after the runtime event bus is stable, design hooks as event consumers instead of reinventing another event model

The current branch has already completed the "event system foundation" part of the first step, but there is not yet a real hook mounting layer. Therefore, the goal here is not to redesign events, but to add an extensible, interceptable, and externally mountable HookManager on top of the existing implementation.

## Comparison with External Projects

### OpenClaw

OpenClaw's extension capabilities are divided into three layers:

- Internal hooks: directory discovery, running inside the Gateway process
- Plugin hooks: plugins register hooks at runtime, also in-process
- Webhooks: external systems trigger Gateway actions through HTTP, outside the process

Points worth learning from:

- It has two paths: "mounting inside the project" and "mounting outside the project"
- Hooks are configuration-driven and can be enabled or disabled
- External entry points have clear security boundaries and a mapping layer

Points not recommended for direct copying:

- OpenClaw's hooks / plugin hooks / webhooks are three separate routing systems, which would be too heavy at GoGoClaw's current scale
- HTTP webhooks are better suited for "events entering the system" and are not suitable as the foundation for "synchronously intercepting the agent loop"

### pi-mono

pi-mono's core ideas are closer to the current branch:

- Extensions are unified into an extension API
- Events are divided into observational and mutable types
- Some phases allow `transform` / `block` / `replace`
- Extension code mainly executes in-process
- RPC mode bridges UI interaction to out-of-process clients

Points worth learning from:

- Do not mix "observation" and "interception" into one interface
- Allow structured actions to be returned instead of only callbacks
- Out-of-process communication exposes only the necessary protocol and does not leak the entire internal object graph

## Current Branch Status

### Existing Capabilities

The current branch already has the foundation for a hook system:

- `pkg/events` defines the runtime event envelope, kind, scope, source, severity, and fan-out bus
- `pkg/agent/event_payloads.go` retains agent domain payloads
- `runTurn()` in `pkg/agent/loop.go` already emits events at nodes such as turn, llm, tool, interrupt, follow-up, and summary
- `pkg/agent/steering.go` already supports steering, graceful interrupt, and hard abort
- `pkg/agent/turn.go` already maintains turn phase, recovery points, active turn, and abort status

### Existing Gaps

The gaps at the time of the early design included HookManager, Before/After LLM, Before/After Tool, approval-style hooks, and sub-turn integration. The current implementation already covers HookManager, LLM/Tool interception, and approval for the main turn; sub-turn events have been integrated into the runtime event bus.

### A Key Reality

The `#1316` description mentions a tool execution strategy of "parallel reads, serial writes", but the current `runTurn()` implementation has already converged on "sequential execution + checking steering / interrupt after each tool". Therefore, the hook design should not depend on a future parallel model; it should first be compatible with the current sequential execution and leave room for adding a `ReadOnlyIndicator` later.

## Design Principles

- Hooks must be built on the `pkg/events` runtime event bus and turn context
- The runtime event bus is responsible for broadcasting, and HookManager is responsible for interception; the responsibilities of the two are separated
- Mounting inside the project should be simple; mounting outside the project must use IPC
- Observational hooks must not block the loop; intercepting hooks must have timeouts
- Cover the main turn first; do not try to complete sub-turn in one pass
- Do not add a second user-event naming system; new observation points should uniformly use `pkg/events.Kind`

## Overall Architecture

It is divided into three layers:

1. `pkg/events` runtime event bus
   Responsible for broadcasting read-only events and covering runtime components such as agent, channel, gateway, bus, and MCP

2. `HookManager`
   Responsible for managing hooks, ordering, timeouts, and error isolation, and performing synchronous interception at explicit checkpoints in `runTurn()`

3. `HookMount`
   Responsible for two mounting methods:
   - In-process Go hooks
   - Out-of-process IPC hooks

In other words:

- The runtime event bus is "what happened"
- HookManager is "who can intervene"
- HookMount is "where these hooks come from"

## Hook Classification

It is not recommended to design all hooks as `OnEvent(evt)`.

It is recommended to split them into two types.

### 1. Observational

Only consume events and do not modify the flow:

```go
type EventObserver interface {
    OnRuntimeEvent(ctx context.Context, evt events.Event) error
}
```

This type of hook can simply subscribe to the runtime event bus.

Applicable scenarios:

- Audit logs
- Metrics reporting
- Debug traces
- Forwarding events to external UI / TUI / Web panels

### 2. Intercepting

Triggered only at a few explicit nodes and allowed to return actions:

```go
type LLMInterceptor interface {
    BeforeLLM(ctx context.Context, req *LLMRequest) HookDecision[*LLMRequest]
    AfterLLM(ctx context.Context, resp *LLMResponse) HookDecision[*LLMResponse]
}

type ToolInterceptor interface {
    BeforeTool(ctx context.Context, call *ToolCall) HookDecision[*ToolCall]
    AfterTool(ctx context.Context, result *ToolResultView) HookDecision[*ToolResultView]
}

type ToolApprover interface {
    ApproveTool(ctx context.Context, req *ToolApprovalRequest) ApprovalDecision
}
```

`HookDecision` here uniformly supports:

- `continue`
- `modify`
- `deny_tool`
- `abort_turn`
- `hard_abort`

## Minimum Exposed Hook Surface

V1 does not need to turn every runtime event kind into an interceptable point.

It is recommended to expose only these synchronous hooks:

- `before_llm`
- `after_llm`
- `before_tool`
- `after_tool`
- `approve_tool`

The remaining nodes continue to be exposed as read-only events:

- `agent.turn.start`
- `agent.turn.end`
- `agent.llm.request`
- `agent.llm.response`
- `agent.tool.exec_start`
- `agent.tool.exec_end`
- `agent.tool.exec_skipped`
- `agent.steering.injected`
- `agent.follow_up.queued`
- `agent.interrupt.received`
- `agent.context.compress`
- `agent.session.summarize`
- `agent.error`

`subturn_*` retains its names in V1, but there is no commitment that it will always fire until the sub-turn migration is complete.

## In-Project Mounting

Internal mounting must have as little friction as possible.

It is recommended to provide two equivalent methods, both backed by HookManager.

### Option A: Explicit Code Mounting

```go
al.MountHook(hooks.Named("audit", &AuditHook{}))
```

Suitable for:

- Built-in hooks in the repository
- Unit tests
- Feature flag control

### Option B: Built-in Registry

```go
func init() {
    hooks.RegisterBuiltin("audit", func() hooks.Hook {
        return &AuditHook{}
    })
}
```

Enable at startup based on configuration:

```json
{
  "hooks": {
    "builtins": {
      "audit": { "enabled": true }
    }
  }
}
```

This is lighter than OpenClaw's directory scanning and fits a Go project better.

## Out-of-Project Mounting

This is a hard requirement of this design.

It is recommended that V1 use:

- `JSON-RPC over stdio`

Reasons:

- Simplest across platforms
- Does not depend on an extra port
- Very suitable for "having GoGoClaw start an external hook process"
- Better suited for synchronous interception than HTTP webhooks

### External Hook Process Model

GoGoClaw starts an external process and runs the protocol over its stdin/stdout.

Configuration example:

```json
{
  "hooks": {
    "processes": {
      "review-gate": {
        "enabled": true,
        "transport": "stdio",
        "command": ["uvx", "gogoclaw-hook-reviewer"],
        "observe": ["turn_start", "turn_end", "tool_exec_end"],
        "intercept": ["before_tool", "approve_tool"],
        "timeout_ms": 5000
      }
    }
  }
}
```

### Protocol Boundary

Do not expose internal Go structs directly to IPC.

It is recommended to define stable protocol objects:

- `HookHandshake`
- `HookEventNotification`
- `BeforeLLMRequest`
- `AfterLLMRequest`
- `BeforeToolRequest`
- `AfterToolRequest`
- `ApproveToolRequest`
- `HookDecision`

Among them:

- Observational events use notifications, fire-and-forget
- Intercepting events use request/response, waiting synchronously

### Why stdio Instead of HTTP Webhooks Directly

Because the two serve different purposes:

- HTTP webhooks are better suited for "external systems delivering events to GoGoClaw"
- stdio/RPC is better suited for "GoGoClaw synchronously asking an external hook within a turn whether to rewrite / allow / deny"

If an OpenClaw-style webhook is needed in the future, it can be added as an independent entry layer, and external events can then be converted into inbound messages or steering, rather than directly replacing hook IPC.

## Hook Execution Order

It is recommended to use a unified ordering rule:

- Built-in in-process hooks first
- External IPC hooks afterward
- Within the same group, execute in ascending `priority`

Reasons:

- Built-in hooks have lower latency and are suitable for basic normalization
- External hooks are better suited for approval, auditing, and organization-level policies

## Timeout and Error Strategy

### Observational

- Default timeout: `500ms`
- On timeout or error: log it and continue the main flow

### Intercepting

- `before_llm` / `after_llm` / `before_tool` / `after_tool`: default `5s`
- `approve_tool`: default `60s`

Timeout behavior:

- Ordinary interception: `continue`
- Approval: `deny`

This point should directly follow the safety preference of `#1316`.

## Integration Points with the Current Branch

### Direct Reuse

- Event definitions: `pkg/agent/events.go`
- Event broadcasting: `pkg/agent/eventbus.go`
- Active turn / interrupt / rollback: `pkg/agent/turn.go`
- Event emission points: `pkg/agent/loop.go`

### To Be Added

- `pkg/agent/hooks.go`
  - Hook interfaces
  - HookDecision / ApprovalDecision
  - HookManager

- `pkg/agent/hook_mount.go`
  - Built-in hook registration
  - External process hook registration

- `pkg/agent/hook_ipc.go`
  - stdio JSON-RPC bridge

- `pkg/agent/hook_types.go`
  - Stable IPC payloads

### To Be Modified

- `pkg/agent/loop.go`
  - Insert HookManager calls before and after key LLM and tool paths

- `pkg/tools/base.go`
  - Optionally add `ReadOnlyIndicator`

- `pkg/tools/spawn.go`
- `pkg/tools/subagent.go`
  - Keep the current state for now
  - Integrate `subturn_*` hooks after the sub-turn migration

## A Data Flow Better Suited to the Current Branch

### Observation Path

```text
runTurn() -> emitEvent() -> runtime event bus -> observers
```

### Interception Path

```text
runTurn()
  -> HookManager.BeforeLLM()
  -> Provider.Chat()
  -> HookManager.AfterLLM()
  -> HookManager.BeforeTool()
  -> HookManager.ApproveTool()
  -> tool.Execute()
  -> HookManager.AfterTool()
```

In other words:

- Observers do not change the existing `emitEvent()`
- Interceptors are inserted directly into the `runTurn()` hot path

## User-Visible Configuration

Proposed addition:

```json
{
  "hooks": {
    "enabled": true,
    "builtins": {},
    "processes": {},
    "defaults": {
      "observer_timeout_ms": 500,
      "interceptor_timeout_ms": 5000,
      "approval_timeout_ms": 60000
    }
  }
}
```

V1 does not implement complex automatic discovery.

Reasons:

- The current branch's focus is to stabilize the foundation
- Directory scanning, installers, and scaffolding can come later
- Making both in-repository and out-of-repository mounting possible is more important than "a complete management experience"

## Recommended V1 Scope

### Must Do

- HookManager
- In-process mounting
- stdio IPC mounting
- Observer hooks
- `before_tool` / `after_tool` / `approve_tool`
- `before_llm` / `after_llm`

### Can Be Deferred

- Hook CLI management commands
- Hook automatic discovery
- Unix socket / named pipe transport
- Sub-turn hook lifecycle
- Read-only parallel grouping
- Mapping entry point from webhooks to inbound messages

## Phased Implementation

### Phase 1

- Introduce HookManager
- Support in-process observers + interceptors
- Integrate only the main turn first

### Phase 2

- Introduce the `stdio` external hook process bridge
- Support organization-level approval / auditing / parameter rewriting

### Phase 3

- Migrate `SubagentManager` to `runTurn/sub-turn`
- Connect `agent.subturn.spawn` / `agent.subturn.end` / `agent.subturn.result_delivered`

### Phase 4

- Add `ReadOnlyIndicator` as needed
- Use a unified read-only parallel strategy for both the main turn and sub-turns

## Final Conclusion

The most suitable approach for the current GoGoClaw branch is neither directly copying OpenClaw's hooks nor fully replicating pi-mono's extension system. Instead, it is:

- Use the `pkg/events` runtime event bus as the read-only observation surface
- Use a new `HookManager` as the synchronous interception surface
- Mount in-project hooks directly through Go objects
- Mount out-of-project hooks through `stdio JSON-RPC` process communication

This approach has three benefits:

- Consistent with `#1796`: hooks are merely a consumption layer above the runtime event bus
- Consistent with the current `refactor/agent` implementation: there is no need to overturn the existing event system
- It satisfies both hard requirements: "simple mounting inside the repository" and "out-of-repository process communication mounting"
