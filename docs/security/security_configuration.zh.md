# 安全配置

## 概述

GoGoClaw 支持将敏感数据（API 密钥、token、secret、密码）存入 `.security.yml` 文件，从而与主配置分离。这样通过以下方式提升安全性：

1. **关注点分离**：配置设置与密钥位于不同文件
2. **更易于共享**：主配置可以在不暴露敏感数据的情况下共享
3. **更好的版本控制**：应将 `.security.yml` 添加到 `.gitignore`
4. **灵活部署**：不同环境可以使用不同的安全文件

## 文件结构

```
~/.gogoclaw/
├── config.json          # Main configuration (safe to share)
└── .security.yml         # Security data (never share)
```

## 工作原理

安全配置通过**直接字段映射**工作，而不是通过 `ref:` 字符串引用。系统会自动从 `.security.yml` 加载值，并将其应用到 `config.json` 中对应字段。

### 关键点：

- `.security.yml` 中的值会自动映射到配置中的对应字段
- 映射基于字段名和结构，而不是引用字符串
- 如果某个值存在于 `.security.yml`，它会**覆盖** `config.json` 中的值
- 你完全可以省略 `config.json` 中的敏感字段（推荐）

## 安全配置结构

### 完整示例：.security.yml

```yaml
# Model API Keys
# All models MUST use `api_keys` (plural) array format
# Even a single key must be provided as an array with one element
model_list:
  gpt-5.4:
    api_keys:
      - "sk-proj-your-actual-openai-key-1"
      - "sk-proj-your-actual-openai-key-2"  # Optional: Multiple keys for failover
  claude-sonnet-4.6:
    api_keys:
      - "sk-ant-your-actual-anthropic-key"  # Single key in array format

# Channel Tokens
channel_list:
  telegram:
    settings:
      token: "your-telegram-bot-token"
  feishu:
    settings:
      app_secret: "your-feishu-app-secret"
      encrypt_key: "your-feishu-encrypt-key"
      verification_token: "your-feishu-verification-token"
  discord:
    settings:
      token: "your-discord-bot-token"
  weixin:
    settings:
      token: "your-weixin-token"
  qq:
    settings:
      app_secret: "your-qq-app-secret"
  pico:
    settings:
      token: "your-pico-token"

# Web Tool API Keys
web:
  brave:
    api_keys:
      - "BSAyour-brave-api-key-1"
      - "BSAyour-brave-api-key-2"  # Optional: Multiple keys for failover
  tavily:
    api_keys:
      - "tvly-your-tavily-api-key"  # Single key in array format
  perplexity:
    api_keys:
      - "pplx-your-perplexity-api-key"  # Single key in array format
  glm_search:
    api_key: "your-glm-search-api-key"  # GLMSearch uses single key format (not array)
  baidu_search:
    api_key: "your-baidu-search-api-key"

# Skills Registry Tokens
skills:
  github:
    token: "your-github-token"
  clawhub:
    auth_token: "your-clawhub-auth-token"
```

## 使用方法

### 步骤 1：创建 .security.yml

创建或复制安全文件：
```bash
cp security.example.yml ~/.gogoclaw/.security.yml
```

### 步骤 2：填入实际值

编辑 `~/.gogoclaw/.security.yml`，将占位值替换为实际 API 密钥和 token。

### 步骤 3：设置合适权限

```bash
chmod 600 ~/.gogoclaw/.security.yml
```

### 步骤 4：简化 config.json（推荐）

现在可以从 `config.json` 中移除敏感字段，因为它们会从 `.security.yml` 加载：

**之前：**
```json
{
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api.openai.com/v1",
      "api_keys": ["sk-your-actual-api-key-here"]
    }
  ],
  "channel_list": {
    "telegram": {
      "enabled": true,
      "type": "telegram",
      "token": "1234567890:ABCdefGHIjklMNOpqrsTUVwxyz"
    }
  }
}
```

**之后：**
```json
{
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api.openai.com/v1"
      // api_key is now loaded from .security.yml
    }
  ],
  "channel_list": {
    "telegram": {
      "enabled": true,
      "type": "telegram"
      // token is now loaded from .security.yml
    }
  }
}
```

### 步骤 5：验证

重启 GoGoClaw 并验证其能正确加载：
```bash
gogoclaw --version
```

## 字段映射规则

### 模型

**.security.yml 中：**
```yaml
model_list:
  <model_name>:
    api_keys:
      - "key-1"
      - "key-2"
```

**映射：**
- 字段 `api_keys`（数组）映射到模型的 API 密钥
- `<model_name>` 必须与 `config.json` 中的 `model_name` 字段匹配
- 支持带索引的名称（例如 `"gpt-5.4:0"`）——系统还会尝试基础名称（`"gpt-5.4"`）

### 渠道

渠道密钥在 `.security.yml` 和 `config.json` 中使用相同名称：

**.security.yml 中：**
```yaml
channel_list:
  telegram:
    settings:
      token: "value"
  feishu:
    settings:
      app_secret: "value"
      encrypt_key: "value"
      verification_token: "value"
  discord:
    settings:
      token: "value"
```

**映射：**
- `channel_list.telegram.settings.token` → `config.channel_list.telegram.settings.token`
- `channel_list.feishu.settings.app_secret` → `config.channel_list.feishu.settings.app_secret`
- 等等。

### Web 工具

**Brave、Tavily、Perplexity、Kagi：**
```yaml
web:
  brave:
    api_keys:
      - "key-1"
      - "key-2"
  kagi:
    api_keys:
      - "your-kagi-api-key"
```
- 使用 `api_keys`（复数）数组格式

**GLMSearch：**
```yaml
web:
  glm_search:
    api_key: "single-key-here"
```
- 使用 `api_key`（单数）单字符串格式

**BaiduSearch：**
```yaml
web:
  baidu_search:
    api_key: "your-key"
```
- 使用 `api_key`（单数）单字符串格式

### Skills

**.security.yml 中：**
```yaml
skills:
  github:
    token: "value"
  clawhub:
    auth_token: "value"
```

## API 密钥格式

### 模型 - 单个密钥

使用只含一个元素的数组格式：
```yaml
model_list:
  gpt-5.4:
    api_keys:
      - "sk-your-key"
```

### 模型 - 多个密钥（负载均衡与故障转移）

使用包含多个元素的数组格式：
```yaml
model_list:
  gpt-5.4:
    api_keys:
      - "sk-your-key-1"
      - "sk-your-key-2"
      - "sk-your-key-3"
```

**优势：**
- **负载均衡**：请求会分布到多个密钥
- **故障转移**：某个密钥失败时自动切换到另一个密钥
- **限流管理**：将使用量分散到多个密钥
- **高可用性**：减少 API Provider 故障期间的停机时间

### Web 工具（Brave/Tavily/Perplexity/Kagi）- 单个密钥

```yaml
web:
  brave:
    api_keys:
      - "BSA-your-key"
  kagi:
    api_keys:
      - "your-kagi-api-key"
```

### Web 工具（Brave/Tavily/Perplexity/Kagi）- 多个密钥

```yaml
web:
  brave:
    api_keys:
      - "BSA-key-1"
      - "BSA-key-2"
  kagi:
    api_keys:
      - "kagi-key-1"
      - "kagi-key-2"
```

### Web 工具（GLMSearch/BaiduSearch）- 仅支持单个密钥

```yaml
web:
  glm_search:
    api_key: "your-glm-key"  # Single string (NOT array)
  baidu_search:
    api_key: "your-baidu-key"  # Single string (NOT array)
```

## 模型名称匹配

系统支持在 `.security.yml` 中进行智能模型名称匹配：

### 示例 1：精确匹配

**config.json：**
```json
{
  "model_name": "gpt-5.4:0"
}
```

**.security.yml（带索引的精确匹配）：**
```yaml
model_list:
  gpt-5.4:0:
    api_keys: ["key-1"]
```

### 示例 2：基础名称匹配

**config.json：**
```json
{
  "model_name": "gpt-5.4:0"
}
```

**.security.yml（不带索引的基础名称）：**
```yaml
model_list:
  gpt-5.4:
    api_keys: ["key-1", "key-2"]
```

两种方式都有效。基础名称匹配允许即使配置使用带索引的模型名称进行负载均衡，也可以在 `.security.yml` 中使用更简单的键。

## 向后兼容

系统保持完全向后兼容：

1. **直接值**：你仍然可以在 `config.json` 中使用直接值（生产环境不推荐）
2. **混合使用**：可以一部分字段放在 `.security.yml`，另一部分放在 `config.json`
3. **可选安全文件**：如果 `.security.yml` 不存在，系统只使用 `config.json` 中的值
4. **覆盖行为**：如果字段同时存在于两个文件中，以 `.security.yml` 的值为准

## 环境变量

你可以使用环境变量覆盖渠道与工具的安全值：

**对于渠道：**
```bash
export GOGOCLAW_CHANNELS_TELEGRAM_TOKEN="token-from-env"
export GOGOCLAW_CHANNELS_FEISHU_APP_SECRET="secret-from-env"
```

**对于 Web 工具：**
```bash
export GOGOCLAW_TOOLS_WEB_BRAVE_API_KEY="key-from-env"
export GOGOCLAW_TOOLS_WEB_BAIDU_API_KEY="baidu-key-from-env"
```

环境变量具有最高优先级，会覆盖 `config.json` 和 `.security.yml` 中的值。

模式为：`GOGOCLAW_<SECTION>_<KEY>_<FIELD>`，用下划线分隔各路径段并转换为大写。

## OAuth Token 存储

`auth.json` 中的 OAuth token 与配置密钥遵循相同的 `enc://` 加密策略：

- 设置 `GOGOCLAW_KEY_PASSPHRASE` 后，access token 与 refresh token 会以 AES-256-GCM 加密后落盘。密钥由 passphrase 与 SSH key（`GOGOCLAW_SSH_KEY_PATH`，默认 `~/.ssh/gogoclaw_ed25519.key`）派生。
- 未设置 passphrase 时，token 以旧版明文格式保存并记录警告日志；OAuth 登录不受影响。
- 旧明文存储可正常加载；配置 passphrase 后，下次保存会自动升级为密文。
- `auth.json` 始终以 `0600` 权限写入。

## 安全最佳实践

1. **永远不要将 `.security.yml` 提交**到版本控制
2. **添加到 .gitignore**：确保 `.security.yml` 位于 `.gitignore` 文件中
3. **设置文件权限**：`chmod 600 ~/.gogoclaw/.security.yml`
4. **为不同环境使用不同密钥**（开发、预发布、生产）
5. **定期轮换密钥**并更新 `.security.yml`
6. **安全备份**：加密包含 `.security.yml` 的备份。注意配置迁移会自动创建带日期戳的备份（例如 `config.json.20260330.bak` 和 `.security.yml.20260330.bak`）
7. **审查访问权限**：确保只有授权用户可以读取该文件

## API

### loadSecurityConfig

```go
func loadSecurityConfig(cfg *Config, securityPath string) error
```

将 `.security.yml` 中的值合并到现有 `Config`。文件不存在时视为空安全配置。

### saveSecurityConfig

```go
func saveSecurityConfig(securityPath string, cfg *Config) error
```

将配置中的敏感值保存到 `.security.yml`，权限为 `0o600`。

### 渠道密钥格式

渠道密钥使用与 `config.json` 相同的嵌套结构：`channel_list.<name>.settings.<field>`。加载器也接受现有文件使用的旧版扁平渠道密钥格式。

### securityPath

```go
func securityPath(configPath string) string
```

返回相对于配置文件的 `.security.yml` 路径。

## 示例：完整配置

### config.json

```json
{
  "version": 3,
  "agents": {
    "defaults": {
      "workspace": "~/gogoclaw-workspace",
      "model_name": "gpt-5.4"
    }
  },
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api.openai.com/v1"
    },
    {
      "model_name": "claude-sonnet-4.6",
      "model": "anthropic/claude-sonnet-4.6",
      "api_base": "https://api.anthropic.com/v1"
    }
  ],
  "channel_list": {
    "telegram": {
      "enabled": true,
      "type": "telegram"
    }
  },
  "tools": {
    "web": {
      "brave": {
        "enabled": true
      }
    }
  }
}
```

### .security.yml

```yaml
model_list:
  gpt-5.4:
    api_keys:
      - "sk-proj-actual-openai-key-1"
      - "sk-proj-actual-openai-key-2"
  claude-sonnet-4.6:
    api_keys:
      - "sk-ant-actual-anthropic-key"

channel_list:
  telegram:
    settings:
      token: "1234567890:ABCdefGHIjklMNOpqrsTUVwxyz"

web:
  brave:
    api_keys:
      - "BSAactualbravekey-1"
      - "BSAactualbravekey-2"
  tavily:
    api_keys:
      - "tvly-your-tavily-key"
  glm_search:
    api_key: "your-glm-key"
  baidu_search:
    api_key: "your-baidu-key"
```

## 测试

运行安全配置测试：

```bash
go test ./pkg/config -run TestSecurityConfig
```

## 故障排查

### 错误："failed to load security config"

- 确认 `.security.yml` 与 `config.json` 位于同一目录
- 检查 YAML 语法是否有效（可使用 YAML 校验器）
- 确保文件权限允许读取

### 错误："model security entry not found"

- 确保 `config.json` 中的模型名称与 `.security.yml` 中的名称完全匹配
- 检查 `.security.yml` 中是否存在 `model_list` 部分
- 对于带索引名称的模型（例如 `"gpt-5.4:0"`），确保使用精确名称，或检查不带索引的基础名称
- 确认 YAML 结构正确（缩进合适）

### 多个 API 密钥不生效

- 确保在 `.security.yml` 中为模型和 Web 工具（GLMSearch/BaiduSearch 除外）使用 `api_keys`（复数）
- 检查 YAML 中数组格式是否正确（使用短横线并保持正确缩进）
- 记住：模型、Brave、Tavily、Perplexity、Kagi 必须使用 `api_keys`（数组格式）
- GLMSearch 和 BaiduSearch 必须使用 `api_key`（单字符串格式）

### 负载均衡/故障转移问题

- 验证 `api_keys` 数组中的所有 API 密钥都有效
- 检查所有密钥是否具有相同的速率限制和权限
- 监控日志，查看正在使用和失败的密钥
- 确保 YAML 中的 `api_keys` 数组格式正确

### 密钥未生效

- 检查 `.security.yml` 是否与 `config.json` 位于同一目录
- 验证文件权限允许读取（`chmod 600 ~/.gogoclaw/.security.yml`）
- 确保 YAML 结构与预期格式一致
- 检查字段名是否有拼写错误（区分大小写）
- 验证模型/渠道名称是否完全匹配（区分大小写）

## 迁移指南

### 步骤 1：备份配置

系统在保存迁移后的配置前会自动创建带日期戳的备份（例如 `config.json.20260330.bak` 和 `.security.yml.20260330.bak`）。如果你更希望手动备份：

```bash
cp ~/.gogoclaw/config.json ~/.gogoclaw/config.json.backup
```

### 步骤 2：创建 .security.yml

```bash
cp security.example.yml ~/.gogoclaw/.security.yml
```

### 步骤 3：填入 API 密钥

编辑 `~/.gogoclaw/.security.yml`，将占位值替换为实际密钥。

### 步骤 4：从 config.json 中移除敏感字段

从 `config.json` 中移除或注释掉敏感字段：
- `model_list` 条目中的 `api_key` 字段
- `channels` 中的 `token` 字段
- `tools.web` 中的 `api_key` 字段
- `tools.skills` 中的 `token`/`auth_token` 字段

### 步骤 5：设置合适权限

```bash
chmod 600 ~/.gogoclaw/.security.yml
```

### 步骤 6：测试

```bash
gogoclaw --version
```

### 步骤 7：验证功能

测试模型和渠道，确保一切正常工作。

### 步骤 8：清理（可选）

如果一切正常，可以删除备份：
```bash
rm ~/.gogoclaw/config.json.backup
# Also remove auto-generated date-stamped backups if desired:
rm ~/.gogoclaw/config.json.20*.bak ~/.gogoclaw/.security.yml.20*.bak
```

## 高级：加密 API 密钥

GoGoClaw 支持加密安全文件中的 API 密钥，以提供额外保护。

### 设置

1. 通过环境变量设置口令：
```bash
export GOGOCLAW_CREDENTIAL_PASSPHRASE="your-secure-passphrase"
```

2. 保存配置时，API 密钥会自动加密：
```go
SaveConfig(path, config)
```

### 加密格式

加密密钥存储为：
```yaml
model_list:
  gpt-5.4:
    api_keys:
      - "enc://encrypted-base64-string"
```

系统在加载配置时会在运行时自动解密密钥。

### 优势

- 额外一层安全保护
- 密钥静态加密
- 口令可以与配置文件分开管理

### 重要说明

- 始终安全备份口令
- 如果丢失口令，将无法访问加密密钥
- 使用强而唯一的口令
- 永远不要将口令提交到版本控制
