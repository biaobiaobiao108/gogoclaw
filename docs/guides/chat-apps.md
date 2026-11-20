# Chat Apps

Back to [README](../README.md). GoGoClaw currently supports Telegram, Discord, Feishu / Lark, Weixin, QQ, and its built-in Pico WebSocket channel.

All channels use the same top-level shape:

```json
{
  "channel_list": {
    "telegram": {
      "enabled": true,
      "type": "telegram",
      "settings": {
        "token": "YOUR_BOT_TOKEN"
      }
    }
  }
}
```

Common channel fields are `enabled`, `type`, `allow_from`, `reasoning_channel_id`, `group_trigger`, `typing`, and `placeholder`. Channel-specific fields stay under `settings`. Secrets entered in the Web console are stored in `.security.yml`.

## Telegram

Create a bot with `@BotFather`, then configure:

```json
{
  "channel_list": {
    "telegram": {
      "enabled": true,
      "type": "telegram",
      "settings": {
        "token": "YOUR_BOT_TOKEN",
        "use_markdown_v2": false
      }
    }
  }
}
```

Use `allow_from` with your Telegram user ID to restrict access. Optional settings include `proxy`, `base_url`, `streaming`, and `media_group_delay_ms`. See [Telegram channel](../channels/telegram/README.md).

Put proxies in `proxy`, not `base_url`. Using `http://user:password@host:port` as the Bot API endpoint makes `getUpdates` return an empty response and usually appears as `decode json: unexpected end of JSON input` in logs.

## Discord

Create a bot in the Discord Developer Portal and enable the required Message Content intent:

```json
{
  "channel_list": {
    "discord": {
      "enabled": true,
      "type": "discord",
      "settings": {
        "token": "YOUR_BOT_TOKEN",
        "mention_only": true
      }
    }
  }
}
```

`proxy` is optional. See [Discord channel](../channels/discord/README.md).

## Feishu / Lark

Create an app on the Feishu Open Platform, enable the Bot capability, publish it, then configure:

```json
{
  "channel_list": {
    "feishu": {
      "enabled": true,
      "type": "feishu",
      "settings": {
        "app_id": "cli_xxx",
        "app_secret": "YOUR_APP_SECRET",
        "is_lark": false
      }
    }
  }
}
```

Feishu uses WebSocket / SDK mode, so it does not require a public callback URL. Set `is_lark` for Lark tenants. See [Feishu channel](../channels/feishu/README.md).

## Weixin

Authenticate with the official Tencent iLink flow:

```bash
gogoclaw auth weixin
```

Scan the QR code. GoGoClaw stores the returned token and automatically writes the scanner's iLink `ilink_user_id` into `allow_from`. The Web console exposes the same login flow. Optional settings include `account_id`, `base_url`, `cdn_base_url`, and `proxy`. See [Weixin channel](../channels/weixin/README.md).

If a binding succeeds but messages get no reply, confirm that `allow_from` contains the real `from_user_id` (normally ending in `@im.wechat`), not a WeChat nickname. Rebind once in the Web console for accounts created before automatic allowlisting was added.

## QQ

Create a QQ bot on the [QQ Open Platform](https://q.qq.com/) and copy its App ID and App Secret. The secret is normally shown only once.

```json
{
  "channel_list": {
    "qq": {
      "enabled": true,
      "type": "qq",
      "settings": {
        "app_id": "YOUR_APP_ID",
        "app_secret": "YOUR_APP_SECRET",
        "send_markdown": false
      }
    }
  }
}
```

Optional limits include `max_message_length` and `max_base64_file_size_mib`. Some newly created bots are creator-only until sandbox or group access is configured on the QQ platform. See [QQ channel](../channels/qq/README.md).

## Pico

Pico is the built-in WebSocket channel used by the Web console. GoGoClaw creates its token automatically when the gateway starts, so no manual setup is normally required.

For custom clients, enable `allow_token_query` only when the client cannot send the token through the supported header. Restrict browser origins with `allow_origins` instead of allowing arbitrary sites.

## Gateway

Start all configured channels with:

```bash
gogoclaw gateway
```

The gateway listens on `gateway.host`:`gateway.port` (default `localhost:18790`). In production, run it through the container image and expose only the ports you need.
