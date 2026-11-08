# Channel Runtime

GoGoClaw ships six channel implementations:

| Package | Type | Transport |
| --- | --- | --- |
| `discord` | `discord` | Discord Gateway |
| `feishu` | `feishu` | Feishu / Lark WebSocket SDK |
| `pico` | `pico` | Native WebSocket protocol used by the Web console |
| `qq` | `qq` | QQ Bot Gateway |
| `telegram` | `telegram` | Long polling |
| `weixin` | `weixin` | Tencent iLink API |

The gateway registers every implementation in `pkg/gateway/gateway.go`. A configuration entry is active only when its `enabled` field is true and its `type` resolves to a registered factory.

## Lifecycle

Every channel implements the `Channel` interface:

```go
type Channel interface {
    Name() string
    Start(context.Context) error
    Stop(context.Context) error
    Send(context.Context, bus.OutboundMessage) ([]string, error)
}
```

Optional capabilities are expressed as small interfaces, including streaming, typing, placeholders, media delivery, tool feedback, approval, and delivery acknowledgement. The channel manager detects these interfaces at runtime instead of requiring every adapter to implement every feature.

## Configuration

Channel configuration is nested:

```json
{
  "channel_list": {
    "telegram": {
      "enabled": true,
      "type": "telegram",
      "settings": {
        "token": "..."
      }
    }
  }
}
```

Common fields remain on the channel object. Adapter-specific fields live under `settings` and decode into the typed settings struct registered in `pkg/config/config_channel.go`. Secret fields use `SecureString` and are persisted separately in `.security.yml`.

## Manager Responsibilities

- Start, stop, and hot-reload channels without restarting unrelated channels.
- Route inbound messages to the shared message bus.
- Resolve outbound targets and split oversized messages.
- Coalesce stream updates and avoid stale placeholder edits.
- Isolate slow or failing channels with bounded send and start operations.
- Track typing and reaction cleanup.

## Adding A Channel

1. Add a settings struct and register it in the channel settings factory.
2. Add the channel constant and default configuration.
3. Implement `Channel` and any required optional capability interfaces.
4. Register a factory from the package `init()` function.
5. Add a blank import in `pkg/gateway/gateway.go`.
6. Add the channel to the Web catalog only after the backend implementation is complete.
7. Add unit tests for configuration decoding, message splitting, stop behavior, and any transport-specific parsing.

Do not add public catalog entries or documentation for a channel that cannot be constructed and started by the gateway.
