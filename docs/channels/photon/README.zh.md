> 返回 [README](../../../README.md)

# Photon iMessage

Photon 频道通过 [Photon Spectrum](https://photon.codes/) 把 GoGoClaw 接入 iMessage。Apple 消息线路由 Photon 提供，GoGoClaw 使用 Go 直接连接 Photon 公开的 gRPC API，不需要 Mac、公网 webhook、内网穿透、Bun sidecar 或 `ffmpeg`。

## 配置

先从 Photon Dashboard 获取 `Project ID` 和 `Project Secret`，然后配置：

```json
{
  "channel_list": {
    "photon": {
      "enabled": true,
      "type": "photon",
      "allow_from": ["+15551234567"],
      "typing": { "enabled": true },
      "placeholder": { "enabled": true, "text": ["Thinking..."] },
      "settings": {
        "project_id": "sp-...",
        "project_secret": "YOUR_PROJECT_SECRET",
        "disable_read_receipts": false
      }
    }
  }
}
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `enabled` | 是 | 是否启用 Photon 频道。 |
| `allow_from` | 否 | 允许访问的 E.164 手机号、邮箱或 `*`；留空时拒绝所有发送者。 |
| `project_id` | 是 | Photon Dashboard 中的 Spectrum Project ID。 |
| `project_secret` | 是 | 同一项目生成的 Project Secret，使用安全字段存储。 |
| `disable_read_receipts` | 否 | 收到消息后不自动标记为已读。 |
| `group_trigger` | 否 | 和其他频道共用的仅提及或前缀触发规则。 |
| `typing.enabled` | 否 | 显示 iMessage 原生输入状态。 |
| `placeholder.enabled` | 否 | 发送可编辑的等待提示。 |
| `streaming.enabled` | 否 | 使用原生 iMessage 编辑实现尽力而为的流式回复。 |

`project_secret` 不会出现在频道配置接口响应中，也不会写入 `config.json`；Web 控制台会将它保存在 `.security.yml`。`cloud_url` 仅作为隐藏的开发环境覆盖项存在，正常配置无需填写。

## 设置流程

1. 登录 [Photon Dashboard](https://app.photon.codes/)。
2. 创建项目，启用 Spectrum，并绑定持有 iMessage 线路的手机号。
3. 生成或轮换 Project Secret。
4. 在 GoGoClaw 的 **iMessage** 频道页填入 Project ID 和 Project Secret。
5. 在 `allow_from` 中添加允许访问的发送者，例如 `+15551234567`。只有确实公开服务时才填写 `*`。
6. 启用频道并保存。

网关会获取并自动刷新短期线路 token，遇到临时线路发现故障时会重试。入站消息先保存到 `sessions/sessions.db`，随后才更新 `sessions/photon-state.json` 中的事件游标。新会话和待投递回复记录接收消息的项目与线路，重启后仍沿该线路发送。既有会话保留在原来的历史记录中。

## 功能

- iMessage 文本双向收发，保留原生富链接。
- 私聊和群聊，复用统一的 `allow_from` 与 `group_trigger` 行为。
- 入站图片、语音、音频、视频和文件附件，通过正常媒体存储和媒体工具处理。
- 出站图片、语音、音频、视频和文件附件，并支持说明文字。
- 原生输入状态、可编辑等待提示、消息编辑、撤回和 👀 Tapback。
- 使用 iMessage 编辑实现尽力而为的流式回复，带保守节流。
- 默认启用已读回执，可通过配置关闭。
- 与其他消息频道一致的持久文本投递、重启恢复和有限重试。

频道使用 `pkg/channels/photon/proto/` 中固定的 Photon 公开 iMessage 协议，并在 `pkg/channels/photon/internal/` 下生成 Go 客户端。生产路径是原生 Go，不启动运行时 sidecar。

## 限制

- Photon 免费共享线路有每日消息额度；在共享线路上，新号码必须先给线路发送过消息，线路才能真正发起新会话。
- iMessage 编辑和撤回受 Apple 的时间窗口与次数限制。流式默认关闭；启用时应使用较保守的更新间隔。
- 入站和出站附件遵循 Agent 配置的媒体大小上限，默认 20 MiB。更大的入站附件只传递元数据。
- Photon 仍需要 GoGoClaw 主机能够主动访问外网 HTTPS/gRPC；但不需要公网回调端口。

## 故障排查

- **Token 请求失败**：确认 Project ID 和 Project Secret 属于同一个 Photon 项目，并确认项目已经启用 Spectrum。
- **新号码发消息不回复**：共享线路需要对方先给分配的 iMessage 线路发送消息。
- **能收到消息但不回复**：检查 `allow_from`。空列表会拒绝所有发送者，配置值必须匹配发送者手机号或邮箱地址。
- **群聊不回复**：开启 `group_trigger.mention_only` 后，需要提及配置的机器人名称或地址；共享运行时也支持配置前缀触发。
- **没有流式效果**：需要显式设置 `settings.streaming.enabled`。即使中间编辑被节流或拒绝，最终回复仍会发送。
- **消息一直显示已送达**：清除 `disable_read_receipts` 以启用自动已读回执。

## 线上验证

可选的 Photon 集成测试使用 `GOGOCLAW_LIVE_PHOTON_PROJECT_ID` 和 `GOGOCLAW_LIVE_PHOTON_PROJECT_SECRET`。只有在你控制的会话中测试一次性消息、编辑、撤回和图片时，才设置 `GOGOCLAW_LIVE_PHOTON_CHAT_ID`：

```sh
go test -tags integration -run '^TestLiveContract_Photon' -v ./pkg/channels/photon
```

未设置 `GOGOCLAW_LIVE_PHOTON_CHAT_ID` 时会跳过发送测试，但仍会执行 token、线路发现、catch-up 和订阅的只读检查。
