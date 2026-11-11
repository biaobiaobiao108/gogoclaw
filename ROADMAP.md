# GoGoClaw Roadmap

GoGoClaw is a single-instance agent harness with a web console. The README and
`docs/` describe what already ships; this file tracks only remaining work.
Production deployments are container-only, and the container is the runtime
security boundary.

## Runtime reliability and performance

- Track gateway memory between releases against the 20 MB target on compact
  64-bit hosts (512 MB RAM baseline); the benchmark workflow records results.
- Add long-running and multi-session stress scenarios to the evaluation
  harness, including restart recovery under load.
- Extend end-to-end coverage for real channels beyond the manual live-contract
  workflows.

## Security in depth

- Evaluate optional OS-level sandboxing for high-risk tools. In-process path
  checks are guardrails; the container remains the isolation boundary.
- Redact secrets and personal data from logs and tool output by default.
- Harden prompt-injection resistance for tool results and fetched content.

## Connectivity

- Add channels on demand (OneBot, DingTalk, Slack) behind the existing channel
  interface.
- Improve audio and video attachment handling across channels.

## Console

- Raise frontend interaction test coverage beyond the current chat-logic tests.
- Continue splitting the largest console pages into focused components.
- Keep `DESIGN.md` and the implementation in sync.

## Documentation and onboarding

- Keep every `docs/` page paired: English baseline plus `.zh.md` translation.
- Smooth the zero-config first run: detect the environment and guide model and
  channel setup without hand-editing files.