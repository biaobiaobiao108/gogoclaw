# 配置模式版本化指南

## 概述

GoGoClaw 为 `config.json` 使用模式版本化系统，确保配置格式演进时能够顺利升级。

## 版本历史

### 版本 1
- **引入**：初始版本，支持 version 字段
- **变更**：向 Config 结构体添加 `version` 字段
- **迁移**：现有配置不需要结构性变更

### 版本 2
- **引入**：模型启用/禁用支持与渠道配置统一
- **变更**：
  - 向 `ModelConfig` 添加 `enabled` 字段——允许禁用单个模型条目而不删除它们
  - 在 V1→V2 迁移期间，`enabled` 会自动推断：带有 API 密钥或保留名称 `local-model` 的模型处于启用状态；其他模型默认禁用
  - 迁移旧渠道字段：Discord `mention_only` → `group_trigger.mention_only`，OneBot `group_trigger_prefix` → `group_trigger.prefixes`
  - V0 配置现在直接迁移到 CurrentVersion（V2），而不是先经过 V1
  - `makeBackup()` 现在使用仅日期的后缀（例如 `config.json.20260330.bak`），并且也会备份 `.security.yml`

### 版本 3
- **引入**：增强类型安全并改进错误处理
- **变更**：
  - 在渠道配置解码中添加 comma-ok 类型断言，以防止潜在 panic
  - 改进 Weixin 渠道配置解码的错误日志
  - 增强安全配置文档和示例
  - 迁移期间将缺失的模型 `enabled` 值持久化为 `false`，以保留 V2 的推断行为
  - **自动迁移**：V2 配置在加载时自动迁移到 V3，无需用户操作
  - **备份**：迁移前，系统会在同一目录创建带日期戳的备份（例如 `config.json.20260413.bak`）
  - **降级风险**：配置一旦迁移到 V3，旧版仅支持 V2 的版本就无法安全加载它。要降级，请从自动创建的备份文件恢复。

## 工作原理

### 自动迁移
当你加载配置文件时：
1. 系统首先从 JSON 中读取 `version` 字段
2. 根据检测到的版本，加载对应的配置结构体（`configV0`、`configV1` 等）
3. 如果加载的版本低于最新版本，则增量应用迁移
4. 保存前，系统会自动创建 `config.json` 和 `.security.yml` 的带日期戳备份
5. 版本号会自动更新
6. 迁移后的配置会自动保存回磁盘

### Version 字段
`config.json` 中的 `version` 字段表示配置模式版本：
- `0` 或缺失：旧版配置（没有 version 字段）
- `1`：上一版本（加载时将自动迁移到 V2）
- `2`：上一版本（加载时将自动迁移到 V3）
- `3`：当前版本

```json
{
  "version": 3,
  "agents": {...},
  ...
}
```

## 添加新迁移

对配置模式进行破坏性变更时：

### 步骤 1：定义新版本结构体

如果结构发生显著变化，为新版本创建新的结构体：

```go
// ConfigV3 represents version 3 config structure
type ConfigV3 struct {
    Version   int             `json:"version"`
    Agents    AgentsConfig    `json:"agents"`
    // ... other fields with new structure
}
```

### 步骤 2：更新当前配置版本

```go
const CurrentVersion = 3  // Increment this
```

### 步骤 3：添加加载函数

```go
// loadConfigV3 loads a version 3 config
func loadConfigV3(data []byte) (*Config, error) {
    cfg := DefaultConfig()

    // Parse to ConfigV3 struct
    var v3 ConfigV3
    if err := json.Unmarshal(data, &v3); err != nil {
        return nil, err
    }

    // Convert to current Config
    cfg.Version = v3.Version
    cfg.Agents = v3.Agents
    // ... map other fields

    return cfg, nil
}
```

### 步骤 4：添加迁移逻辑

```go
func (c *configV2) Migrate() (*Config, error) {
    // Apply V2→V3 structural changes here
    migrated := &c.Config
    migrated.Version = 3
    // Apply structural changes
    return migrated, nil
}
```

### 步骤 5：更新 LoadConfig 的 switch

```go
func LoadConfig(path string) (*Config, error) {
    // ... read file ...

    switch versionInfo.Version {
    case 0:
        cfg, err = loadConfigV0(data)
    case 1:
        cfg, err = loadConfigV1(data)
    case 2:
        cfg, err = loadConfig(data)
    case 3:
        cfg, err = loadConfigV3(data)
    default:
        return nil, fmt.Errorf("unsupported config version: %d", versionInfo.Version)
    }

    // ... migrate and validate ...
}
```

### 步骤 6：测试你的迁移

在 `config_migration_test.go` 中创建一个测试：

```go
func TestMigrateV2ToV3(t *testing.T) {
    // Create a version 3 config
    v3Config := Config{
        Version: 3,
        // ... set up test data
    }

    // Apply migration
    migrated, err := v2Config.Migrate()
    if err != nil {
        t.Fatalf("Migration failed: %v", err)
    }

    // Verify version is updated
    if migrated.Version != 3 {
        t.Errorf("Expected version 3, got %d", migrated.Version)
    }

    // Verify data is preserved/transformed correctly
    // ...
}
```

## 迁移最佳实践

1. **版本专用结构体**：为每个发生结构性变化的版本定义独立结构体
2. **向后兼容**：确保旧配置仍能通过其专用结构体加载
3. **无数据丢失**：迁移应保留所有用户设置
4. **幂等**：多次运行同一迁移应当安全
5. **自动保存**：迁移后的配置会自动保存，以更新用户文件
6. **自动备份**：保存前，系统会创建 `config.json` 和 `.security.yml` 的带日期戳备份
7. **充分测试**：使用真实用户配置文件进行测试
8. **更新默认值**：让 `defaults.go` 与最新模式保持同步

## V2→V3 迁移指南

### 有哪些变更？

版本 3 引入了改进的类型安全和错误处理：

- **类型安全的渠道解码**：所有渠道类型断言现在都使用 comma-ok 模式（`val, ok := v.(*Settings)`），防止 Type 与 Settings 不匹配时发生 panic
- **增强错误日志**：Weixin 渠道现在会在 `GetDecoded()` 失败时记录错误，以与其他渠道保持一致
- **文档修复**：修正 JSON 配置示例中的多余引号

### 自动迁移行为

当你使用 V2 配置文件运行 GoGoClaw 时：

1. **检测**：GoGoClaw 读取 `version` 字段并检测到 V2
2. **备份**：在任何更改之前，创建 `config.json.YYYYMMDD.bak`（例如 `config.json.20260413.bak`）
3. **迁移**：应用 V2→V3 结构性变更（主要属于内部类型安全改进）
4. **保存**：写入更新后的配置，版本为 `"version": 3`
5. **继续**：使用 V3 配置正常启动

**无需用户操作**——迁移会在首次加载时自动发生。

### 备份位置

备份会创建在配置文件所在目录：

- **默认**：`~/.gogoclaw/config.json.20260413.bak`
- **自定义路径**：如果使用 `GOGOCLAW_CONFIG`，备份会创建在该文件旁边
- **安全文件**：`.security.yml` 也会备份为 `.security.yml.YYYYMMDD.bak`

### 降级风险

⚠️ **重要**：配置一旦迁移到 V3，旧版仅支持 V2 的 GoGoClaw 版本就**无法**安全加载它。

**要降级：**

1. 停止 GoGoClaw
2. 恢复备份：
   ```bash
   cp ~/.gogoclaw/config.json.20260413.bak ~/.gogoclaw/config.json
   cp ~/.gogoclaw/.security.yml.20260413.bak ~/.gogoclaw/.security.yml  # if it exists
   ```
3. 使用支持 V2 配置的 GoGoClaw 版本

**替代方案**：手动编辑 `config.json`，将 `"version": 3` 改为 `"version": 2`。这样做可行，因为 V3 变更主要是代码层面的安全性改进，而不是结构性模式变更。

## 迁移示例

### 场景：添加带默认值的新字段

旧配置（版本 2）：
```json
{
  "version": 2,
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4"
    }
  ]
}
```

迁移到版本 3：
```go
func (c *configV2) Migrate() (*Config, error) {
    migrated := &c.Config
    migrated.Version = 3

    // Add new field with default value if not set
    // ...

    return migrated, nil
}
```

新配置（版本 3）：
```json
{
  "version": 3,
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "new_option": true
    }
  ]
}
```

## 故障排查

### 配置未升级
- 检查 `CurrentVersion` 是否已递增
- 确认迁移逻辑处理了目标版本
- 确保 `LoadConfig()` 中调用了 `Migrate()`

### 迁移错误
- 检查错误消息以定位具体迁移失败
- 审查迁移逻辑中的边界情况
- 确保所有必填字段都正确初始化
- 确认源版本的加载函数

### 迁移后数据丢失
- 确保迁移期间复制了所有字段
- 检查迁移是否不必要地用默认值覆盖了已有值
- 审查加载函数中的转换逻辑
- 检查自动备份文件（例如 `config.json.20260330.bak`）以恢复原始数据
