# Provider 架构重构设计

> Issue: #283
> Discussion: #122
> Branch: feat/refactor-provider-by-protocol

## 1. 当前问题

### 1.1 配置结构问题

**当前状态**：每个 Provider 都需要在 `ProvidersConfig` 中有一个预定义字段

```go
type ProvidersConfig struct {
    Anthropic     ProviderConfig `json:"anthropic"`
    OpenAI        ProviderConfig `json:"openai"`
    DeepSeek      ProviderConfig `json:"deepseek"`
    Qwen          ProviderConfig `json:"qwen"`
    Cerebras      ProviderConfig `json:"cerebras"`
    VolcEngine    ProviderConfig `json:"volcengine"`
    // ... every new provider requires changes here
}
```

**问题**：
- 新增 Provider 需要修改 Go 代码（结构体定义）
- `http_provider.go` 中的 `CreateProvider` 函数有 200 多行 switch-case
- 大多数 Provider 都兼容 OpenAI，但代码被重复实现

### 1.2 代码膨胀趋势

最近的 PR 展示了这个问题：

| PR | Provider | 代码变更 |
|----|----------|--------------|
| #365 | Qwen | http_provider.go +17 行 |
| #333 | Cerebras | http_provider.go +17 行 |
| #368 | Volcengine | http_provider.go +18 行 |

每个兼容 OpenAI 的 Provider 都需要：
1. 修改 `config.go` 添加配置字段
2. 修改 `http_provider.go` 添加 switch case
3. 更新文档

### 1.3 Agent-Provider 耦合

```json
{
  "agents": {
    "defaults": {
      "provider": "deepseek",  // need to know provider name
      "model": "deepseek-chat"
    }
  }
}
```

问题：Agent 需要同时知道 `provider` 和 `model`，增加了复杂度。

---

## 2. 新方案：model_list

### 2.1 核心原则

受 [LiteLLM](https://docs.litellm.ai/docs/proxy/configs) 设计启发：

1. **以模型为中心**：用户关心的是模型，而不是 Provider
2. **协议前缀**：使用 `protocol/model_name` 格式，例如 `openai/gpt-5.4`、`anthropic/claude-sonnet-4.6`
3. **配置驱动**：新增 Provider 只需要修改配置，不需要修改代码

### 2.2 新配置结构

```json
{
  "model_list": [
    {
      "model_name": "deepseek-chat",
      "model": "openai/deepseek-chat",
      "api_base": "https://api.deepseek.com/v1",
      "api_keys": ["sk-xxx"]
    },
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_keys": ["sk-xxx"]
    },
    {
      "model_name": "claude-sonnet-4.6",
      "model": "anthropic/claude-sonnet-4.6",
      "api_keys": ["sk-xxx"]
    },
    {
      "model_name": "gemini-3-flash",
      "model": "antigravity/gemini-3-flash",
      "auth_method": "oauth"
    },
    {
      "model_name": "my-company-llm",
      "model": "openai/company-model-v1",
      "api_base": "https://llm.company.com/v1",
      "api_keys": ["xxx"]
    }
  ],

  "agents": {
    "defaults": {
      "model": "deepseek-chat",
      "max_tokens": 8192,
      "temperature": 0.7
    }
  }
}
```

### 2.3 Go 结构体定义

```go
type Config struct {
    ModelList []ModelConfig `json:"model_list"`  // new
    Providers ProvidersConfig `json:"providers"`  // old, deprecated

    Agents   AgentsConfig   `json:"agents"`
    Channels ChannelsConfig `json:"channels"`
    // ...
}

type ModelConfig struct {
    // Required
    ModelName string `json:"model_name"`  // user-facing name (alias)
    Model     string `json:"model"`       // protocol/model, e.g., openai/gpt-5.4

    // Common config
    APIBase   string `json:"api_base,omitempty"`
    APIKey    string `json:"api_key,omitempty"`
    Proxy     string `json:"proxy,omitempty"`

    // Special provider config
    AuthMethod  string `json:"auth_method,omitempty"`   // oauth, token
    ConnectMode string `json:"connect_mode,omitempty"`  // stdio, grpc

    // Optional optimizations
    RPM            int    `json:"rpm,omitempty"`              // rate limit
    MaxTokensField string `json:"max_tokens_field,omitempty"` // max_tokens or max_completion_tokens
}
```

### 2.4 协议识别

通过 `model` 字段中的前缀识别协议：

| 前缀 | 协议 | 说明 |
|--------|----------|-------------|
| `openai/` | 兼容 OpenAI | 最常见，包括 DeepSeek、Qwen、Groq 等 |
| `anthropic/` | Anthropic | Claude 系列专用 |
| `antigravity/` | Antigravity | Google Cloud Code Assist |
| `gemini/` | Gemini | Google Gemini 原生 API |

---

## 3. 设计理由

### 3.1 解决的问题

| 问题 | 旧方案 | 新方案 |
|---------|--------------|--------------|
| 添加兼容 OpenAI 的 Provider | 修改 3 处代码 | 添加一条配置 |
| Agent 指定模型 | 需要 provider + model | 只需要 model |
| 代码重复 | 每个 Provider 重复实现逻辑 | 共享协议实现 |
| 多 Agent 支持 | 复杂 | 天然兼容 |

### 3.2 多 Agent 兼容性

```json
{
  "model_list": [...],

  "agents": {
    "defaults": {
      "model": "deepseek-chat"
    },
    "coder": {
      "model": "gpt-5.4",
      "system_prompt": "You are a coding assistant..."
    },
    "translator": {
      "model": "claude-sonnet-4.6"
    }
  }
}
```

每个 Agent 只需要指定 `model`（对应 `model_list` 中的 `model_name`）。

### 3.3 行业对比

**LiteLLM**（最成熟的开源 LLM Proxy）使用了类似设计：

```yaml
model_list:
  - model_name: gpt-4o
    litellm_params:
      model: openai/gpt-5.4
      api_key: xxx
  - model_name: my-custom
    litellm_params:
      model: openai/custom-model
      api_base: https://my-api.com/v1
```

---

## 4. 迁移计划

### 4.1 阶段 1：兼容期（v1.x）

同时支持 `providers` 和 `model_list`：

```go
func (c *Config) GetModelConfig(modelName string) (*ModelConfig, error) {
    // Prefer new config
    if len(c.ModelList) > 0 {
        return c.findModelByName(modelName)
    }

    // Backward compatibility with old config
    if !c.Providers.IsEmpty() {
        logger.Warn("'providers' config is deprecated, please migrate to 'model_list'")
        return c.convertFromProviders(modelName)
    }

    return nil, fmt.Errorf("model %s not found", modelName)
}
```

### 4.2 阶段 2：警告期（v1.x 后期）

- 在启动时打印更醒目的警告
- 提供自动迁移脚本
- 在文档中将 `providers` 标记为已弃用

### 4.3 阶段 3：移除期（v2.0）

- 完全移除 `providers` 支持
- 移除 `agents.defaults.provider` 字段
- 仅支持 `model_list`

### 4.4 配置迁移示例

**旧配置**：
```json
{
  "providers": {
    "deepseek": {
      "api_keys": ["sk-xxx"],
      "api_base": "https://api.deepseek.com/v1"
    }
  },
  "agents": {
    "defaults": {
      "provider": "deepseek",
      "model": "deepseek-chat"
    }
  }
}
```

**新配置**：
```json
{
  "model_list": [
    {
      "model_name": "deepseek-chat",
      "model": "openai/deepseek-chat",
      "api_base": "https://api.deepseek.com/v1",
      "api_key": "sk-xxx"
    }
  ],
  "agents": {
    "defaults": {
      "model": "deepseek-chat"
    }
  }
}
```

---

## 5. 实施清单

### 5.1 配置层

- [ ] 添加 `ModelConfig` 结构体
- [ ] 添加 `Config.ModelList` 字段
- [ ] 实现 `GetModelConfig(modelName)` 方法
- [ ] 实现旧配置兼容转换
- [ ] 添加 `model_name` 唯一性校验

### 5.2 Provider 层

- [ ] 创建 `pkg/providers/factory/` 目录
- [ ] 实现 `CreateProviderFromModelConfig()`
- [ ] 将 `http_provider.go` 重构为 `openai/provider.go`
- [ ] 保持旧 `CreateProvider()` 的向后兼容

### 5.3 测试

- [ ] 新配置单元测试
- [ ] 旧配置兼容性测试
- [ ] 集成测试

### 5.4 文档

- [ ] 更新 README
- [ ] 更新 config.example.json
- [ ] 编写迁移指南

---

## 6. 风险与缓解

| 风险 | 缓解措施 |
|------|------------|
| 破坏现有配置 | 兼容期让旧配置继续可用 |
| 用户迁移成本 | 提供自动迁移脚本 |
| 特殊 Provider 不兼容 | 保留 `auth_method` 等扩展字段 |

---

## 7. 参考资料

- [LiteLLM Config Documentation](https://docs.litellm.ai/docs/proxy/configs)
- [One-API GitHub](https://github.com/songquanpeng/one-api)
- Discussion #122: Refactor Provider Architecture
