# 迁移指南：从 `providers` 到 `model_list`

本指南说明如何从旧版 `providers` 配置迁移到新的 `model_list` 格式。

## 为什么迁移？

新的 `model_list` 配置提供了多项优势：

- **零代码添加 Provider**：仅通过配置添加兼容 OpenAI 的 Provider
- **负载均衡**：为同一个模型配置多个端点
- **显式 Provider 解析**：优先使用 `provider` + 原生 `model`，在需要时兼容旧版 `provider/model`
- **更清晰的配置**：以模型为中心，而不是以厂商为中心

## 时间线

| 版本 | 状态 |
|---------|--------|
| v1.x | 引入 `model_list`，`providers` 已弃用但仍可使用 |
| v1.x+1 | 醒目弃用警告，提供迁移工具 |
| v2.0 | 移除 `providers` 配置 |

## 迁移前后

### 之前：旧版 `providers` 配置

```json
{
  "providers": {
    "openai": {
      "api_key": "sk-your-openai-key",
      "api_base": "https://api.openai.com/v1"
    },
    "anthropic": {
      "api_key": "sk-ant-your-key"
    },
    "deepseek": {
      "api_key": "sk-your-deepseek-key"
    }
  },
  "agents": {
    "defaults": {
      "provider": "openai",
      "model": "gpt-5.4"
    }
  }
}
```

### 之后：新的 `model_list` 配置

```json
{
  "version": 3,
  "model_list": [
    {
      "model_name": "gpt4",
      "provider": "openai",
      "model": "gpt-5.4",
      "api_keys": ["sk-your-openai-key"],
      "api_base": "https://api.openai.com/v1"
    },
    {
      "model_name": "claude-sonnet-4.6",
      "provider": "anthropic",
      "model": "claude-sonnet-4.6",
      "api_keys": ["sk-ant-your-key"]
    },
    {
      "model_name": "deepseek",
      "provider": "deepseek",
      "model": "deepseek-chat",
      "api_keys": ["sk-your-deepseek-key"]
    }
  ],
  "agents": {
    "defaults": {
      "model_name": "gpt4"
    }
  }
}
```

> **注意**：在当前配置版本中，省略 `enabled` 字段会被视为 `true`。在 V1→V2 迁移期间，旧状态则通过推断得到：带有 API 密钥或名为 `local-model` 的模型处于启用状态，其他条目则被禁用。若要禁用当前模型条目而不删除它，请显式设置 `"enabled": false`。

## Provider / 模型解析

首选格式：

```json
{
  "provider": "openai",
  "model": "gpt-5.4"
}
```

旧版兼容格式：

```json
{
  "model": "openai/gpt-5.4"
}
```

解析规则：

1. 如果设置了 `provider`，GoGoClaw 会原样发送 `model`。
2. 如果省略 `provider`，GoGoClaw 会把 `model` 中第一个 `/` 之前的部分视为 Provider，并把第一个 `/` 之后的所有内容视为运行时模型 ID。

示例：

| 配置 | 解析出的 Provider | 向上游发送的模型 |
|--------|-------------------|---------------------|
| `"provider": "openai", "model": "gpt-5.4"` | `openai` | `gpt-5.4` |
| `"model": "openai/gpt-5.4"` | `openai` | `gpt-5.4` |
| `"provider": "openrouter", "model": "google/gemini-2.0-flash-exp:free"` | `openrouter` | `google/gemini-2.0-flash-exp:free` |
| `"model": "openrouter/google/gemini-2.0-flash-exp:free"` | `openrouter` | `google/gemini-2.0-flash-exp:free` |

## ModelConfig 字段

| 字段 | 是否必需 | 说明 |
|-------|----------|-------------|
| `model_name` | 是 | 面向用户的模型别名 |
| `provider` | 否 | 首选 Provider 标识符。设置后，`model` 会原样发送 |
| `model` | 是 | 设置 `provider` 时为原生模型 ID，省略 `provider` 时为旧版 `provider/model` |
| `api_base` | 否 | API 端点 URL |
| `api_keys` | 否 | API 认证密钥（数组；支持多个密钥用于负载均衡） |
| `enabled` | 否 | 此模型条目是否启用。当前配置中省略值默认为 `true`；V1→V2 迁移时会根据 API 密钥和 `local-model` 名称推断旧状态。设为 `false` 可禁用。 |
| `proxy` | 否 | HTTP 代理 URL |
| `auth_method` | 否 | 认证方式：`oauth`、`token` |
| `connect_mode` | 否 | CLI Provider 的连接模式：`stdio`、`grpc` |
| `rpm` | 否 | 每分钟请求数限制 |
| `max_tokens_field` | 否 | max tokens 的字段名 |
| `request_timeout` | 否 | HTTP 请求超时，单位为秒；`<=0` 使用默认值 `120s` |

> **注意**：V2 配置中已**移除** `api_key`（单数形式）。仅支持 `api_keys`（数组）。从 V0/V1 迁移时，`api_key` 和 `api_keys` 都会自动合并到新的 `api_keys` 数组中。

## 负载均衡

有两种配置负载均衡的方式：

### 方案 1：在 `api_keys` 中使用多个 API 密钥（推荐）

```json
{
  "model_list": [
    {
      "model_name": "gpt4",
      "provider": "openai",
      "model": "gpt-5.4",
      "api_keys": ["sk-key1", "sk-key2", "sk-key3"],
      "api_base": "https://api.openai.com/v1"
    }
  ]
}
```

或者通过 `.security.yml`：

```yaml
model_list:
  gpt4:
    api_keys:
      - "sk-key1"
      - "sk-key2"
      - "sk-key3"
```

### 方案 2：多个模型条目

```json
{
  "model_list": [
    {
      "model_name": "gpt4",
      "provider": "openai",
      "model": "gpt-5.4",
      "api_keys": ["sk-key1"],
      "api_base": "https://api1.example.com/v1"
    },
    {
      "model_name": "gpt4",
      "provider": "openai",
      "model": "gpt-5.4",
      "api_keys": ["sk-key2"],
      "api_base": "https://api2.example.com/v1"
    },
    {
      "model_name": "gpt4",
      "provider": "openai",
      "model": "gpt-5.4",
      "api_keys": ["sk-key3"],
      "api_base": "https://api3.example.com/v1"
    }
  ]
}
```

当你请求模型 `gpt4` 时，请求会使用轮询选择分发到所有三个端点。

## 添加新的兼容 OpenAI 的 Provider

使用 `model_list` 时，添加新 Provider 不需要修改代码：

```json
{
  "model_list": [
    {
      "model_name": "my-custom-llm",
      "provider": "openai",
      "model": "my-model-v1",
      "api_keys": ["your-api-key"],
      "api_base": "https://api.your-provider.com/v1"
    }
  ]
}
```

只需将 `provider` 设为 `openai`（或其他受支持的 Provider），并提供你的 Provider 的 API base URL。

## 向后兼容

在迁移期间，你现有的 V0/V1 配置会自动迁移到 V2：

1. 如果 `model_list` 为空且 `providers` 有数据，系统会在内部自动转换
2. V0/V1 配置中的 `api_key`（单数）和 `api_keys`（数组）都会合并到新的 `api_keys` 数组
3. 会记录一条弃用警告：`"providers config is deprecated, please migrate to model_list"`
4. 所有现有功能保持不变

## 迁移检查清单

- [ ] 确定你当前正在使用的所有 Provider
- [ ] 为每个 Provider 创建 `model_list` 条目
- [ ] 优先使用显式的 `provider` 值和原生模型 ID
- [ ] 更新 `agents.defaults.model_name`，使其引用新的 `model_name`
- [ ] 测试所有模型是否正常工作
- [ ] 删除或注释掉旧的 `providers` 部分

## 故障排查

### 模型未找到错误

```
model "xxx" not found in model_list or providers
```

**解决方案**：确保 `model_list` 中的 `model_name` 与 `agents.defaults.model_name` 中的值匹配。

### 未知协议错误

```
unknown provider "xxx" in model "xxx/model-name"
```

**解决方案**：使用受支持的 `provider` 值，或正确使用旧版 `provider/model` 兼容格式。参见 [Provider / 模型解析](#provider--model-resolution)。

### 缺少 API 密钥错误

```
api_key or api_base is required for HTTP-based protocol "xxx"
```

**解决方案**：为基于 HTTP 的 Provider 提供 `api_keys` 和/或 `api_base`。

## 需要帮助？

- [GitHub Issues](https://github.com/biaobiaobiao108/gogoclaw/issues)
- [Discussion #122](https://github.com/biaobiaobiao108/gogoclaw/discussions/122)：原始提案
