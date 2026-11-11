# Provider 架构重构 - 测试套件摘要

本文汇总为 Provider 架构重构设计的完整测试套件。

## 测试文件结构

```
pkg/
├── config/
│   ├── model_config_test.go      # US-001, US-002: ModelConfig struct and GetModelConfig tests
│   └── migration_test.go         # US-003: Backward compatibility and migration tests
├── providers/
│   ├── factory_test.go           # US-004, US-005: Provider factory tests
│   └── factory_provider_test.go  # Factory provider integration tests
```

---

## 测试用例清单

### 1. `pkg/config/model_config_test.go` - 配置解析测试

| 测试名称 | 目的 | PRD 参考 |
|-----------|---------|---------------|
| `TestModelConfig_Parsing` | 验证 ModelConfig JSON 解析 | US-001 |
| `TestModelConfig_ModelListInConfig` | 验证 Config 中的 model_list 解析 | US-001 |
| `TestModelConfig_Validation` | 验证必填字段校验 | US-001 |
| `TestConfig_GetModelConfig_Found` | 验证 GetModelConfig 能找到模型 | US-002 |
| `TestConfig_GetModelConfig_NotFound` | 验证 GetModelConfig 返回错误 | US-002 |
| `TestConfig_GetModelConfig_EmptyModelList` | 验证空 model_list 的处理 | US-002 |
| `TestConfig_BackwardCompatibility_ProvidersToModelList` | 验证旧配置转换 | US-003 |
| `TestConfig_DeprecationWarning` | 验证弃用警告 | US-003 |
| `TestModelConfig_ProtocolExtraction` | 验证协议前缀提取 | US-004 |
| `TestConfig_ModelNameUniqueness` | 验证 model_name 唯一性 | US-001 |

### 2. `pkg/config/migration_test.go` - 迁移测试

| 测试名称 | 目的 | PRD 参考 |
|-----------|---------|---------------|
| `TestConvertProvidersToModelList_OpenAI` | OpenAI 配置转换 | US-003 |
| `TestConvertProvidersToModelList_Anthropic` | Anthropic 配置转换 | US-003 |
| `TestConvertProvidersToModelList_MultipleProviders` | 多 Provider 转换 | US-003 |
| `TestConvertProvidersToModelList_EmptyProviders` | 空 providers 的处理 | US-003 |
| `TestConvertProvidersToModelList_GitHubCopilot` | GitHub Copilot 转换 | US-003 |
| `TestConvertProvidersToModelList_Antigravity` | Antigravity 转换 | US-003 |
| `TestGenerateModelName_*` | 模型名称生成 | US-003 |
| `TestHasProvidersConfig_*` | 检测旧配置是否存在 | US-003 |
| `TestValidateMigration_*` | 迁移校验 | US-003 |
| `TestMigrateConfig_DryRun` | 试运行迁移 | US-003 |
| `TestMigrateConfig_Actual` | 实际迁移 | US-003 |

### 3. `pkg/providers/registry_test.go` - 负载均衡测试

| 测试名称 | 目的 | PRD 参考 |
|-----------|---------|---------------|
| `TestModelRegistry_SingleConfig` | 单个配置返回相同结果 | US-006 |
| `TestModelRegistry_RoundRobinSelection` | 3 个配置的轮询选择 | US-006 |
| `TestModelRegistry_RoundRobinTwoConfigs` | 2 个配置的轮询选择 | US-006 |
| `TestModelRegistry_ConcurrentAccess` | 并发访问线程安全 | US-006 |
| `TestModelRegistry_RaceDetection` | 数据竞争检测 | US-006 |
| `TestModelRegistry_ModelNotFound` | 模型未找到错误 | US-006 |
| `TestModelRegistry_EmptyRegistry` | 空注册表处理 | US-006 |
| `TestModelRegistry_MultipleModels` | 多模型注册 | US-006 |
| `TestModelRegistry_MixedSingleAndMultiple` | 单个/多个配置混合 | US-006 |
| `TestModelRegistry_CaseSensitiveModelNames` | 大小写敏感性 | US-006 |

### 4. `pkg/providers/factory/factory_test.go` - Provider 工厂测试

| 测试名称 | 目的 | PRD 参考 |
|-----------|---------|---------------|
| `TestCreateProviderFromConfig_OpenAI` | 创建 OpenAI Provider | US-004 |
| `TestCreateProviderFromConfig_OpenAIDefault` | 默认 openai 协议 | US-004 |
| `TestCreateProviderFromConfig_Anthropic` | 创建 Anthropic Provider | US-004 |
| `TestCreateProviderFromConfig_Antigravity` | 创建 Antigravity Provider | US-004 |
| `TestCreateProviderFromConfig_ClaudeCLI` | 创建 Claude CLI Provider | US-004 |
| `TestCreateProviderFromConfig_CodexCLI` | 创建 Codex CLI Provider | US-004 |
| `TestCreateProviderFromConfig_GitHubCopilot` | 创建 GitHub Copilot Provider | US-004 |
| `TestCreateProviderFromConfig_UnknownProtocol` | 未知协议错误处理 | US-004 |
| `TestCreateProviderFromConfig_MissingAPIKey` | 缺少 API 密钥错误 | US-004 |
| `TestExtractProtocol` | 协议前缀提取 | US-004 |
| `TestCreateProvider_UsesModelList` | 使用 model_list 创建 | US-005 |
| `TestCreateProvider_FallbackToProviders` | 回退到 providers | US-005 |
| `TestCreateProvider_PriorityModelListOverProviders` | model_list 优先级 | US-005 |

### 5. `pkg/providers/integration_test.go` - 端到端集成测试

| 测试名称 | 目的 | PRD 参考 |
|-----------|---------|---------------|
| `TestE2E_OpenAICompatibleProvider_NoCodeChange` | 零代码添加 Provider | 目标 |
| `TestE2E_LoadBalancing_RoundRobin` | 负载均衡实际效果 | US-006 |
| `TestE2E_BackwardCompatibility_OldProvidersConfig` | 旧配置兼容性 | US-003 |
| `TestE2E_ErrorHandling_ModelNotFound` | 模型未找到 | FR-30 |
| `TestE2E_ErrorHandling_MissingAPIKey` | 缺少 API 密钥 | FR-31 |
| `TestE2E_ErrorHandling_InvalidAPIBase` | 无效 API base | FR-30 |
| `TestE2E_ToolCalls_OpenAICompatible` | 工具调用支持 | - |
| `TestE2E_AntigravityProvider` | Antigravity Provider | US-004 |
| `TestE2E_ClaudeCLIProvider` | Claude CLI Provider | US-004 |

### 6. 性能测试

| 测试名称 | 目的 |
|-----------|---------|
| `BenchmarkCreateProviderFromConfig` | Provider 创建性能 |
| `BenchmarkGetModelConfig` | 模型查找性能 |
| `BenchmarkGetModelConfigParallel` | 并发查找性能 |

---

## 运行测试

```bash
# Run all tests
go test ./pkg/... -v

# Run with data race detection
go test ./pkg/... -race

# Run specific package tests
go test ./pkg/config -v
go test ./pkg/providers -v

# Run E2E tests
go test ./pkg/providers -run TestE2E -v

# Run performance tests
go test ./pkg/providers -bench=. -benchmem
```

---

## PRD 验收标准映射

| PRD 验收标准 | 测试用例 |
|------------------------|------------|
| US-001: 添加 ModelConfig 结构体 | `TestModelConfig_Parsing`, `TestModelConfig_Validation` |
| US-001: model_name 唯一 | `TestConfig_ModelNameUniqueness` |
| US-002: GetModelConfig 方法 | `TestConfig_GetModelConfig_*` |
| US-003: 自动转换 providers | `TestConvertProvidersToModelList_*` |
| US-003: 弃用警告 | `TestConfig_DeprecationWarning` |
| US-003: 现有测试通过 | （现有测试文件保持不变） |
| US-004: 协议前缀工厂 | `TestExtractProtocol`, `TestCreateProviderFromConfig_*` |
| US-004: 默认前缀 openai | `TestCreateProviderFromConfig_OpenAIDefault` |
| US-005: CreateProvider 使用工厂 | `TestCreateProvider_*` |
| US-006: 轮询选择 | `TestModelRegistry_RoundRobin*` |
| US-006: 线程安全原子性 | `TestModelRegistry_RaceDetection` |

---

## 推荐实施顺序

1. **阶段 1：配置结构**（US-001、US-002）
   - 实现 `ModelConfig` 结构体
   - 实现 `GetModelConfig` 方法
   - 运行 `model_config_test.go`

2. **阶段 2：协议工厂**（US-004）
   - 实现 `CreateProviderFromConfig`
   - 实现 `ExtractProtocol`
   - 运行 `factory_test.go`

3. **阶段 3：负载均衡**（US-006）
   - 实现 `ModelRegistry`
   - 实现轮询选择
   - 运行 `registry_test.go`（带 `-race`）

4. **阶段 4：向后兼容**（US-003、US-005）
   - 实现 `ConvertProvidersToModelList`
   - 重构 `CreateProvider`
   - 运行 `migration_test.go`
   - 验证现有测试通过

5. **阶段 5：端到端验证**
   - 运行 `integration_test.go`
   - 使用 `config.example.json` 进行手动测试
