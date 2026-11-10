# Channel Validation Scope

Passing CI proves that local channel behavior and the listed fixtures passed;
it does not prove that every platform account, permission, credential, or live
API is working.

| Validation layer | What it covers | What it does not cover |
| --- | --- | --- |
| Go channel tests | Adapter logic, payload mapping, parsing, and errors using in-process fakes or local HTTP servers | Live credentials, platform account permissions, network policy, or production rate limits |
| Docker integration suite | Real service wiring for the suites under `integration/suites/`; the current reference suite is MCP Streamable | Chat platform accounts. The suite does not connect to Telegram, Discord, Feishu, Weixin, QQ, or Pico services |
| Live Telegram contract | One explicit Bot API `sendMessage` request and successful message ID using configured bot credentials | Polling/inbound updates, group permissions, media uploads, long-term availability, or rate-limit stress |

The regular Go CI job runs adapter tests across the supported channel packages.
Telegram additionally has a local Bot API HTTP contract test that passes through
the configured telego client and verifies the request path, destination,
HTML-formatted text, and returned message ID. Message splitting and formatting
have deterministic unit tests; manager retry behavior is tested with a fake
channel.

To check Telegram credentials and a real API call, manually run the
`Live Telegram contract` workflow. It sends one visible, timestamped text
message to the configured chat. Set `GOGOCLAW_LIVE_TELEGRAM_BOT_TOKEN` as a
repository secret and `GOGOCLAW_LIVE_TELEGRAM_CHAT_ID` as a repository
variable. Choose a test chat where that message is safe to receive.

Other platform adapters currently have automated local tests but no live
credential smoke workflow. Add a platform-specific opt-in contract only when
there is a dedicated test account and a clear one-message or read-only check.
Live rate-limit behavior is deliberately not stress-tested.
