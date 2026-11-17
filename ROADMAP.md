# GoGoClaw Roadmap

GoGoClaw is a single-instance agent harness with a web console. The README and
`docs/` describe what already ships; this file tracks only remaining work.
Production deployments are container-only, and the container is the runtime
security boundary.

## Runtime reliability and performance

- Use the scheduled 20-minute soak artifact to review RSS, goroutine, SQLite,
  WAL and log growth against the compact-host baseline.
- Keep restart recovery and concurrent-session stress in the reliability
  workflow as the product surface grows.
- Extend end-to-end coverage for real channels beyond the manual live-contract
  workflows.

## Security in depth

- Evaluate optional OS-level sandboxing for high-risk tools. In-process path
  checks are guardrails; the container remains the isolation boundary.
- Redact secrets and personal data from logs and tool output by default.

## Connectivity

- Add channels on demand (OneBot, DingTalk, Slack) behind the existing channel
  interface.
- Improve audio and video attachment handling across channels.

## Console

- Keep the cross-browser console smoke, axe scans, 200% text and reduced-motion
  checks current as settings and upload flows change.
- Keep `DESIGN.md` and the implementation in sync.

## Documentation and onboarding

- Keep every `docs/` page paired: English baseline plus `.zh.md` translation.
- Smooth the zero-config first run: detect the environment and guide model and
  channel setup without hand-editing files.
