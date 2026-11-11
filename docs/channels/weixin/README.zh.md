# 微信个人号频道

> 返回 [README](../../project/README.zh.md)

GoGoClaw 通过腾讯 iLink API 连接个人微信账号。该协议是 1:1 绑定：扫码登录的微信用户就是 Agent 的服务对象。

## 扫码绑定

Web 控制台：进入“频道 → 微信”，点击绑定并扫描二维码。确认后 GoGoClaw 会：

1. 保存 iLink bot token 和 account ID。
2. 把登录响应中的 `ilink_user_id` 写入 `allow_from`。
3. 启用微信频道并重启运行中的 gateway。

CLI：

```bash
gogoclaw auth weixin
```

## 配置结构

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

| 字段 | 说明 |
|---|---|
| `allow_from` | 必须使用 iLink 的 `from_user_id`，不能使用微信昵称 |
| `settings.account_id` | 登录返回的 iLink bot account ID |
| `settings.base_url` | iLink API 地址；区域重定向时使用服务端返回的地址 |
| `settings.cdn_base_url` | 媒体 CDN 地址 |
| `settings.proxy` | 可选 HTTP 代理 |

## 绑定成功但没有回复

查看 `/data/.gogoclaw/logs/gateway.log`。先确认频道已启动，再发送一条**新消息**。如果看到：

```text
Message rejected by allowlist
from_user_id=o9cq...@im.wechat
```

把日志中的精确 `from_user_id` 写入 `allow_from`，然后重启 gateway。新版 Web 绑定会自动完成这一步；升级前绑定的账号建议重新扫码。

如果日志没有入站消息，检查 iLink 是否返回 `Session expired`、`getUpdates API error` 或 token 失效。`allow_from` 留空时会拒绝所有来源；个人号绑定应始终使用绑定用户 ID。只有明确要开放给所有人时才使用 `*`。

## 注意事项

- 重新扫码可能让旧 iLink token 失效。
- 不要把 bot token 写入仓库或截图。
- 避免高频广播、循环触发和违反微信平台规则的行为。
