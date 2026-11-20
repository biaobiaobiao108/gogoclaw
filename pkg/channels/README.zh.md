# 频道运行时

GoGoClaw 当前提供六个频道实现：

| 包 | 类型 | 传输方式 |
| --- | --- | --- |
| `discord` | `discord` | Discord Gateway |
| `feishu` | `feishu` | 飞书 / Lark WebSocket SDK |
| `pico` | `pico` | Web 控制台使用的原生 WebSocket 协议 |
| `qq` | `qq` | QQ Bot Gateway |
| `telegram` | `telegram` | 长轮询 |
| `weixin` | `weixin` | 腾讯 iLink API |

网关在 `pkg/gateway/gateway.go` 中注册全部实现。配置项只有在 `enabled` 为 true，且 `type` 能找到已注册工厂时才会启用。

## 生命周期

每个频道实现 `Channel` 接口：

```go
type Channel interface {
    Name() string
    Start(context.Context) error
    Stop(context.Context) error
    Send(context.Context, bus.OutboundMessage) ([]string, error)
}
```

流式输出、输入状态、占位消息、媒体发送、工具反馈、审批和投递确认由独立的小接口表达。频道管理器在运行时检测这些能力，不要求每个适配器实现全部功能。

## 配置

频道配置使用嵌套结构：

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

通用字段保留在频道对象上，适配器字段放在 `settings` 中，并由 `pkg/config/config_channel.go` 注册的类型结构解码。敏感字段使用 `SecureString`，单独保存到 `.security.yml`。

## 管理器职责

- 启停和热更新频道，不影响无关频道。
- 将入站消息路由到共享消息总线。
- 解析出站目标并拆分超长消息。
- 合并流式更新，避免过期的占位消息编辑。
- 用有界操作隔离启动缓慢或发送失败的频道。
- 清理输入状态和临时 reaction。

## 新增频道

1. 添加设置结构，并注册到频道设置工厂。
2. 添加频道常量与默认配置。
3. 实现 `Channel` 和需要的能力接口。
4. 从包 `init()` 注册工厂。
5. 在 `pkg/gateway/gateway.go` 添加匿名导入。
6. 只有后端实现完整后，才能加入 Web 频道目录。
7. 为配置解码、消息拆分、停止行为和传输解析添加单元测试。

不要为无法由网关构造和启动的频道添加公开目录项或文档。
