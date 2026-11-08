# 聊天应用

返回 [README](../project/README.zh.md)。GoGoClaw 当前支持 Telegram、Discord、飞书 / Lark、微信、QQ，以及内置的 Pico WebSocket 频道。

所有频道使用同一种配置结构：

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

通用字段包括 `enabled`、`type`、`allow_from`、`reasoning_channel_id`、`group_trigger`、`typing` 和 `placeholder`，频道专属字段放在 `settings` 中。通过 Web 控制台填写的密钥保存在 `.security.yml`。

## Telegram

通过 `@BotFather` 创建机器人：

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

使用 `allow_from` 填入 Telegram 用户 ID 可限制访问。可选设置包括 `proxy`、`base_url`、`streaming` 和 `media_group_delay_ms`。详见 [Telegram 频道](../channels/telegram/README.zh.md)。

代理必须写入 `proxy`，不要写入 `base_url`。把 `http://user:password@host:port` 当作 Bot API 地址会导致 `getUpdates` 返回空响应，日志通常出现 `decode json: unexpected end of JSON input`。

## Discord

在 Discord Developer Portal 创建机器人并启用 Message Content intent：

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

`proxy` 为可选项。详见 [Discord 频道](../channels/discord/README.zh.md)。

## 飞书 / Lark

在飞书开放平台创建应用，启用机器人能力并发布，然后配置：

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

飞书使用 WebSocket / SDK 模式，不需要公网回调地址。Lark 租户请设置 `is_lark`。详见 [飞书频道](../channels/feishu/README.zh.md)。

## 微信

使用腾讯 iLink 官方扫码登录流程：

```bash
gogoclaw auth weixin
```

扫码成功后，GoGoClaw 会把返回的 token 保存到安全配置，并把扫码用户的 iLink `ilink_user_id` 自动写入 `allow_from`。Web 控制台提供同样的登录流程。可选设置包括 `account_id`、`base_url`、`cdn_base_url` 和 `proxy`。详见 [微信频道](../channels/weixin/README.zh.md)。

如果扫码绑定过但没有回复，检查 `allow_from` 是否为真实的 `from_user_id`（通常以 `@im.wechat` 结尾），而不是微信昵称。升级前绑定的账号建议在 Web 控制台重新扫码一次。

## QQ

在 [QQ 开放平台](https://q.qq.com/) 创建机器人并复制 App ID 和 App Secret。App Secret 通常只显示一次。

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

可选限制包括 `max_message_length` 和 `max_base64_file_size_mib`。部分新建机器人默认仅创建者可用，需要在 QQ 平台配置沙箱或群聊权限。详见 [QQ 频道](../channels/qq/README.zh.md)。

## Pico

Pico 是 Web 控制台使用的内置 WebSocket 频道。网关启动时会自动创建 token，通常无需手动配置。

自定义客户端只有在无法通过支持的请求头发送 token 时才应开启 `allow_token_query`。使用 `allow_origins` 限定浏览器来源，不要允许任意站点。

## 网关

启动全部已配置频道：

```bash
gogoclaw gateway
```

网关监听 `gateway.host`:`gateway.port`，默认是 `localhost:18790`。生产环境请使用容器镜像，并只暴露必要端口。
