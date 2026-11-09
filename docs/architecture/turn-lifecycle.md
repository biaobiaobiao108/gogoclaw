# Turn Lifecycle

Every agent turn follows one explicit state machine. The coordinator in
`pkg/agent/turn_coord.go` owns all phase transitions; pipeline stages execute
work but do not move the turn between phases.

```text
created -> setup -> running <-> tools -> finalizing -> completed
                  |          |             |
                  +----------+-------------+-> failed
                  +----------+-------------+-> aborted
```

## Rules

- Repeated transitions to the current phase are no-ops; every real edge is
  recorded in a bounded history for diagnostics.
- `completed` is reachable only through `finalizing`.
- `failed` and `aborted` are terminal and may be entered from an active phase.
- A turn ends in exactly one terminal phase, set by the coordinator's deferred
  finish path. Panics are recorded as failed before cleanup runs.
- Terminal phases never transition back to an active phase.

## Async Delivery

Async sub-turn and tool results do not have a separate polling path. Producers
push an `asyncDelivery` into the owning turn's queue. The coordinator consumes
that queue once before the next model call. If the turn has stopped or the queue
is full, the result is parked and published as a follow-up inbound turn after
the old turn has been unregistered.

## Evaluation Harness

`pkg/harness` runs scripted scenarios against the real `AgentLoop`. It records
provider calls, executed tool order, runtime events, final history, and outbound
messages, then evaluates declarative trajectory expectations. Provider and tool
faults (errors, panics, delays, context cancellation) are injected without
modifying production dependencies.
