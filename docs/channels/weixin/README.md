# WeChat Personal Channel

GoGoClaw connects to a personal WeChat account through Tencent's iLink API. The binding is 1:1: the WeChat user who completes QR login is the user served by the agent.

## QR Binding

In the web console, open **Channels > WeChat**, click bind, and scan the QR code. After confirmation, GoGoClaw:

1. Saves the iLink bot token and account ID.
2. Writes the returned `ilink_user_id` into `allow_from`.
3. Enables the channel and restarts a running gateway.

CLI equivalent:

```bash
gogoclaw auth weixin
```

## Configuration

```json
{
  "channel_list": {
    "weixin": {
      "enabled": true,
      "type": "weixin",
      "allow_from": ["o9cq...@im.wechat"],
      "settings": {
        "account_id": "...@im.bot",
        "base_url": "https://ilinkai.weixin.qq.com/",
        "cdn_base_url": "https://novac2c.cdn.weixin.qq.com/c2c",
        "proxy": ""
      }
    }
  }
}
```

| Field | Description |
|---|---|
| `allow_from` | Must use the iLink `from_user_id`, not a WeChat nickname |
| `settings.account_id` | iLink bot account ID returned by login |
| `settings.base_url` | iLink API endpoint, including a server-returned regional redirect when present |
| `settings.cdn_base_url` | Media CDN endpoint |
| `settings.proxy` | Optional HTTP proxy |

## Binding Succeeds But Messages Get No Reply

Check `/data/.gogoclaw/logs/gateway.log`, confirm the channel started, and send a **new** message. If the log contains:

```text
Message rejected by allowlist
from_user_id=o9cq...@im.wechat
```

put that exact `from_user_id` into `allow_from` and restart the gateway. New web bindings do this automatically; accounts bound before this behavior was added should be rebound once.

If no inbound message appears, check for `Session expired`, `getUpdates API error`, or an invalid token. An empty `allow_from` opens the channel to everyone and emits a security warning; personal bindings should use an explicit user ID.

## Notes

- Rebinding may invalidate the old iLink token.
- Never commit or screenshot the bot token.
- Avoid loops, high-frequency broadcasts, and behavior that violates WeChat platform rules.
