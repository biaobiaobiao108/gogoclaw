# Model Behavior Evaluation

GoGoClaw has two separate kinds of evaluation:

- `make harness-test` exercises deterministic control flow, storage, tool
  behavior, and async delivery using scripted model responses.
- `gogoclaw-model-eval` sends a small number of requests to a real
  OpenAI-compatible provider and checks observable task outcomes.

A successful provider contract request only proves that an endpoint answered.
It does not count as a model behavior pass. The behavior report has the
`model_behavior` type and includes one result for each task:

1. Ask the model to write the exact content `42` through the production
   `write_file` tool. The evaluator checks the tool call and the resulting file
   bytes.
2. Ask the model to remember a code across two turns. The evaluator checks the
   final answer against the expected code.

Each task uses a fresh workspace and session. The prompts and expected outputs
are fixed so runs against the same configured model are comparable. Model
sampling can still vary, so this workflow is an evaluation signal rather than
a deterministic PR gate.

## Configure And Run

Provide these environment variables:

| Variable | Purpose |
| --- | --- |
| `GOGOCLAW_EVAL_API_BASE` | OpenAI-compatible API base URL |
| `GOGOCLAW_EVAL_API_KEY` | Provider API key |
| `GOGOCLAW_EVAL_MODEL` | Model identifier accepted by that endpoint |

Run locally and save the report:

```bash
go run ./cmd/gogoclaw-model-eval -report build/evaluations/model-behavior.json
```

The command exits non-zero if a task fails. The report contains no API key or
raw request headers. It includes final model answers and mismatch details, so
avoid publishing reports if you modify the tasks to contain private data.

The `Model behavior evaluation` GitHub workflow is manual and uploads the JSON
report as an artifact. Configure the same variables and secret in the
repository before running it.
