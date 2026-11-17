# Debugging GoGoClaw

GoGoClaw performs multiple complex interactions under the hood for every single request it receives—from routing messages and evaluating complexity, to executing tools and adapting to model failures. Being able to see exactly what is happening is crucial, not just for troubleshooting potential issues, but also for truly understanding how the agent operates.

## File Logging And Rotation

Set `GOGOCLAW_LOG_FILE` to write logs to a file. File logs rotate automatically and are also written to the console unless console logging is disabled by the runtime.

| Variable | Default | Purpose |
|---|---:|---|
| `GOGOCLAW_LOG_MAX_SIZE_MB` | `10` | Rotate after the active file reaches this size. `0` disables size rotation |
| `GOGOCLAW_LOG_ROTATE_HOURS` | `24` | Rotate the active file after this many hours. `0` disables time rotation |
| `GOGOCLAW_LOG_MAX_BACKUPS` | `5` | Number of rotated files to retain |
| `GOGOCLAW_LOG_MAX_AGE_DAYS` | `30` | Delete rotated files older than this many days |

The defaults keep the active log below roughly 10 MB and retain at most five recent backups for up to 30 days.
## Starting GoGoClaw in Debug Mode

To get detailed information about what the agent is doing (LLM requests, tool calls, message routing), you can start the GoGoClaw gateway with the debug flag:

```bash
gogoclaw gateway --debug
# or
gogoclaw gateway -d
```

In this mode, the system will format the logs extensively and display previews of system prompts and tool execution results.

## Disabling Log Truncation (Full Logs)

By default, GoGoClaw truncates very long strings (such as the *System Prompt* or large JSON output results) in the debug logs to keep the console readable.

If you need to inspect the complete output of a command or the exact payload sent to the LLM model, you can use the `--no-truncate` flag.

**Note:** This flag *only* works when combined with the `--debug` mode.

```bash
gogoclaw gateway --debug --no-truncate

```

When this flag is active, the global truncation function is disabled. This is extremely useful for:

* Verifying the exact syntax of the messages sent to the provider.
* Reading the complete output of tools like `exec`, `web_fetch`, or `read_file`.
* Debugging the session history saved in memory.

## Tool Call Visibility in Debug Logs

When debug mode is active, the agent emits structured log entries at each stage of the tool execution lifecycle. These entries carry a `component=agent` label and use `INFO` or `DEBUG` level depending on the amount of detail:

| Log message | Level | Key fields | Description |
|---|---|---|---|
| `LLM requested tool calls` | INFO | `tools`, `count`, `iteration` | List of tool names the model decided to call |
| `Tool call: <name>(<args>)` | INFO | `tool`, `iteration` | The tool name and a preview of its arguments (truncated to 200 chars) |
| `Sent tool result to user` | DEBUG | `tool`, `content_len` | Fired when a tool result is forwarded to the chat channel |
| `TTL tick after tool execution` | DEBUG | `agent_id`, `iteration` | MCP tool-discovery TTL decrement after each tool round |
| `Async tool completed, publishing result` | INFO | `tool`, `content_len`, `channel` | Only for tools that run asynchronously in the background |

### Reading a tool call log entry

A typical synchronous tool call produces two consecutive lines in the console:

```
[...] [INFO] agent: LLM requested tool calls {tools=[web_search], count=1, iteration=1}
[...] [INFO] agent: Tool call: web_search({"query":"gogoclaw release notes"}) {tool=web_search, iteration=1}
```

The arguments preview is hard-capped at **200 characters** in the logs regardless of the `--no-truncate` flag, because it belongs to the `INFO`-level path. Use `--no-truncate` together with `--debug` to see the full `tools_json` field emitted by the `Full LLM request` DEBUG entry, which contains every tool definition sent to the model.

## Real-Time Tool Feedback in Chat (tool_feedback)

Debug logs are server-side only. If you want the agent to send a visible notification directly into the chat channel every time it executes a tool—useful when sharing the bot with other users or for transparency—enable the `tool_feedback` feature in `config.json`:

```json
{
  "agents": {
    "defaults": {
      "tool_feedback": {
        "enabled": true,
        "max_args_length": 300,
        "separate_messages": true
      }
    }
  }
}
```

When `enabled` is `true`, every tool call sends a short message to the chat before the tool result is returned to the model. The message looks like:

```bash
🔧 `web_search`
{"query": "gogoclaw release notes"}
```


### Options

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Send a chat notification for each tool call |
| `separate_messages` | bool | `false` | Keep every tool feedback update as a separate chat message instead of reusing a single placeholder/progress message |
| `max_args_length` | int | `300` | Maximum characters of the serialised arguments included in the notification |

### Environment variables

Both fields can also be set via environment variables:

```bash
GOGOCLAW_AGENTS_DEFAULTS_TOOL_FEEDBACK_ENABLED=true
GOGOCLAW_AGENTS_DEFAULTS_TOOL_FEEDBACK_MAX_ARGS_LENGTH=300
```

> **Note:** `tool_feedback` is independent of `--debug` mode. It works in production and does not require the gateway to be started with any special flag.

## SQLite Startup Integrity

Each process opens `sessions/sessions.db` through `PRAGMA quick_check(1)` before reading or writing session data. Schema creation runs in a transaction. A failed check refuses the open instead of continuing with a damaged database.

For a full check, backup, or restore, use the `gogoclaw storage` commands. See [SQLite storage recovery](storage-recovery.md).

## Long-Running Soak

`TestProductSoak` is disabled by default and enabled with a duration:

```bash
GOGOCLAW_SOAK_DURATION=20m make soak-test
```

The test runs repeated turns and writes CSV samples for RSS, goroutines, Go heap/sys, SQLite, WAL, and log occupancy. Set `GOGOCLAW_SOAK_REPORT` to choose the CSV path. Optional thresholds are `GOGOCLAW_SOAK_MAX_RSS_GROWTH_MB` and `GOGOCLAW_SOAK_MAX_GOROUTINE_GROWTH`; Linux RSS is reported when available.

The Reliability workflow runs a 20-minute soak and uploads the report as an artifact. It does not run a NAS 72-hour test.
