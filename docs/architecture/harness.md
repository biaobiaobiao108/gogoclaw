# Evaluation Harness

`pkg/harness` evaluates real `AgentLoop` runs through deterministic scenarios.
It is not a second agent implementation: providers, tools, session persistence,
runtime events, and turn state all pass through the production path.

## Scenario Model

A `Scenario` can define:

- One or more user turns in the same session.
- Scripted provider responses, errors, panics, delays, and context waits.
- Mid-turn dynamic steering injection (`InjectSteering` / `SteerOnCall`) and
  interrupts (`InterruptKind: "graceful"` / `"hard"`, `InterruptOnCall`,
  `HardAbortOnCall`).
- Ordinary, delayed, error, panic, context-canceled, and async tools.
- Automated SubTurn spawner wiring on registered tools (`tools.NewSpawnTool`,
  `tools.NewDelegateTool`, `tools.NewSubagentTool`).
- Session-store failures at checked read/write and checkpoint boundaries.
- Async follow-up processing and bus shutdown during follow-up publication.
- A persistent workspace for restart/recovery scenarios.

`Result` records each turn's model calls, tool calls, runtime events, terminal
state, history, outbound messages, and queued async follow-ups. Async tool
completion follows the same unified queue and follow-up rules as production.

## Assertions

`Expect` supports:

- Exact and substring final-answer assertions, including exact empty values.
- Exact/min/max model-call counts, including zero.
- Turn count, iteration count (`iterations`), status, reason, and complete
  phase sequence.
- Tool call ID, name, arguments, status, error, and observed result text.
- Event presence, ordered subsequence, exact sequence, payload substrings, and
  turn IDs.
- Per-model-call message and tool-list assertions.
- Persisted session history substring assertions (`history_contains`).
- Emitted outbound bus message assertions (`outbound_contains`, `no_outbound`).
- Persisted golden-trace comparison.
- Workspace file-state assertions (`workspace_files`, `workspace_files_absent`)
  for tasks whose success is an artifact or other observable final state.
- Provider-reported prompt, completion, and total tokens in suite reports.

Errors fail an expectation by default. Declare an expected failure with
`error_contains`, or use `allow_error` when the error itself is not being
matched.

Every `Verify` call also checks structural invariants: one start and end event
per turn, a `created` lifecycle origin, legal phase transitions, a terminal
phase matching the turn status, and no tool start left without a terminal
record.

## Trace Persistence And Replay

`Result.Snapshot`, `SaveTrace`, `LoadTrace`, and `CompareTrace` produce a
timestamp-free canonical trace for golden tests. Volatile IDs, durations,
workspace paths, timestamps, and machine-specific runtime details are removed.

`RecordPath` records model responses during a run. `ReplayPath` checks the
model, conversation messages, and tool names before returning each recorded
response. Machine-specific system prompt text is ignored for this input check;
conversation history and the tool surface are compared.

Golden traces are timestamp-free and normalize runtime turn IDs and workspace
paths. Update an intentional golden change with
`$env:UPDATE_HARNESS_GOLDEN='1'; go test ./pkg/harness -run TestGoldenBasicTurn`.

Go scenarios can supply a real provider. Pair it with a state assertion or a
custom test expectation to judge task completion from observable effects;
scripted provider scenarios remain useful for deterministic execution-path
regressions. `ProductionIngress` sends test messages through the message bus
and `AgentLoop.Run`, including final outbound delivery. `ConcurrentSetup` can
exercise multiple sessions on one loop while retaining the runtime event trace.

## Suites And CLI

`RunSuite` and `RunSuiteWithOptions` return a structured `Report` containing
each scenario's pass/fail result and canonical trace. Scenarios can run
sequentially or concurrently with bounded concurrency (`SuiteOptions.Parallel`,
`SuiteOptions.Concurrency`), and filtered by regex (`SuiteOptions.Filter`).

The standalone CLI runs JSON scenarios and exits non-zero on failure:

```bash
go run ./cmd/gogoclaw-harness \
  -scenario scenario.json \
  -filter "login.*" \
  -parallel -concurrency 4 \
  -v \
  -report report.json
```

The `-scenario` flag accepts a single scenario JSON file, a suite JSON file
(`{"scenarios": [...]}`), or a directory containing JSON scenario files.
Supported JSON tool kinds are `text`, `error`, `panic`, and `async_text`.
JSON scenarios can also configure `session_faults` (`get_history`,
`add_message`, `set_checkpoint`, `save`, etc.), mid-turn steering
(`inject_steering`), and interrupts (`interrupt_kind`) without writing Go test
code.
Go-based custom scenarios can additionally use `SessionFault`, `ToolFactory`,
and `Setup` for hook, MCP, channel, or other integration-specific faults.

## Commands

```bash
make harness-test
```

The target runs the harness under the race detector and also compiles the CLI.
