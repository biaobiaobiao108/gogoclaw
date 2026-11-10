# Agent Correctness, Performance, and Cost Governance

GoGoClaw treats model usage, budgets, and tool outcomes as runtime facts instead of relying on model self-reporting.

## Token budgets

Budgets live under `agents.defaults.usage_governance`:

```json
{
  "agents": {
    "defaults": {
      "usage_governance": {
        "session_token_budget": 2000000,
        "daily_token_budget": 10000000,
        "on_budget_exceeded": "stop"
      }
    }
  }
}
```

- `0` disables that budget, so upgrades never impose an unexpected limit.
- Before each primary agent model call, GoGoClaw checks recorded provider usage, in-flight reservations in the same process, the next request's input estimate, and the `max_tokens` output reserve. An over-budget call is not sent.
- `stop` is the default policy. `warn` is only for installations that explicitly accept further spend.
- Session budgets include usage reported by child tasks to their parent session. Daily budgets use natural days in the GoGoClaw process timezone and cover every agent in the instance.
- Daily aggregates are written to the default workspace `sessions.db` as the instance ledger. Other agent workspaces retain only their own session budget buckets.
- If a budget is configured but the session store cannot account for usage, runtime fails closed instead of spending without measurement.

When a provider returns no usage, that call has no verifiable token count. The preflight check still blocks known over-budget requests, while historical totals include only provider-reported usage.

## Usage and cost

Model entries may declare local estimate prices:

```json
{
  "model_name": "fast-model",
  "provider": "openai",
  "model": "gpt-test",
  "input_cost_per_million_usd": 0.15,
  "output_cost_per_million_usd": 0.6
}
```

Prices are local reporting metadata and are never sent to a provider. Aggregates live in the instance `sessions.db`, split by agent, channel, provider, model, model ID, and day. Costs accumulate in integer nano-USD, avoiding both long-running floating-point drift and per-call rounding that would erase low-cost requests. The dashboard's Usage & Cost page shows daily volume and all four dimensions.

## Tool outcomes and final answers

If the latest attempt of a state-changing tool failed or has unknown effects, the final response receives an explicit verification notice and the turn ends with `tool_failure`. A successful retry supersedes an earlier failure only for the same tool and argument fingerprint; unrelated operations keep their own outcomes. Read-only failures do not force the notice.

Policy denial never executes the external operation. If the final response already states the denial reason, GoGoClaw does not append a duplicate notice. If the model still claims success, GoGoClaw adds a denial verification notice. Steering skips and pre-execution cancellations are not misreported as side-effect failures.

This does not prove the external system is in the desired state, but it prevents GoGoClaw from presenting an unqualified success when successful tool evidence is absent.

## Release gate

Run at minimum before release:

```bash
go test -tags goolm,stdjson ./pkg/agent ./pkg/memory ./pkg/session ./web/backend/api
go test -race -tags goolm,stdjson ./pkg/agent ./pkg/memory ./pkg/session ./pkg/harness/...
make harness-test
```

Harness reports expose `model_latency_ms` and `turn_latency_ms` P50/P95 values. Release owners should set thresholds for their hardware and provider region; the repository does not hard-code absolute millisecond limits that cannot hold across environments. Current fixed scenarios cover session restart, tool interruption, parallel tools, async results, and fallback paths. Approval recovery and long-context summarization remain covered by `pkg/agent` tests rather than the full Harness fixture set.

Baseline for a 10,000-message first page:

```bash
go test -tags goolm,stdjson ./pkg/memory -run '^$' -bench BenchmarkSQLiteStoreTranscriptPage10000 -benchmem -count=5
```

## Operational boundaries

- Usage tables contain aggregates, not prompts or tool output.
- Deleting a session removes its budget bucket; daily aggregates remain for instance-level cost reporting.
- Budget units are tokens, not currency. Monetary budgets require stable provider usage and complete model prices.
- In-flight reservations are shared inside one process. Budgets are not shared across multiple GoGoClaw instances; one data directory must still be managed by one GoGoClaw instance.
