# 动态限流

GoGoClaw 会在发送每个请求**之前**执行可配置的按模型请求速率限制，从而避免 LLM Provider API 返回 429 错误。与响应式的冷却/回退系统（在收到 429 *之后*才激活）不同，限流是**主动的**：它让出站 QPS 保持在 Provider 的免费额度或套餐限制之内。

## 工作原理

### 令牌桶算法

每个受限模型都有一个令牌桶：

- **容量** = `rpm`（突发大小等于每分钟限制）
- **补充速率** = `rpm / 60` 个令牌每秒
- 每次 LLM 调用消耗一个令牌；如果桶为空，调用会阻塞，直到补充一个令牌或请求上下文被取消

### 调用链集成

```
AgentLoop.callLLM()
  └─ FallbackChain.Execute()         ← iterate candidates
       ├─ CooldownTracker.IsAvailable()   ← skip if post-429 cooldown active
       ├─ RateLimiterRegistry.Wait()      ← NEW: block until token available
       └─ provider.Chat()                 ← actual LLM HTTP call
```

限流器在冷却检查**之后**、Provider 调用**之前**运行，因此：
- 已经处于冷却状态的候选会被完全跳过（不消耗令牌）
- 可用候选会被限制到配置的 RPM

`ExecuteImage` 中也会应用同样的检查。

### 线程安全

`RateLimiterRegistry` 可以安全地并发使用。每个限流器的令牌桶使用细粒度 mutex，因此并发 goroutine 会各自独立获取自己的令牌。

## 配置

在 `model_list` 中为任意模型设置 `rpm`：

```yaml
model_list:
  - model_name: gpt-4o-free
    provider: openai
    model: gpt-4o
    api_base: https://api.openai.com/v1
    rpm: 3          # max 3 requests per minute
    api_keys:
      - sk-...

  - model_name: claude-haiku
    provider: anthropic
    model: claude-haiku-4-5
    rpm: 60         # 60 rpm (Anthropic free tier)
    api_keys:
      - sk-ant-...

  - model_name: local-llm
    provider: ollama
    model: llama3
    api_base: http://localhost:11434/v1
    # no rpm → unrestricted
```

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `rpm` | `int` | `0` | 每分钟请求数。`0` 表示不限制。 |

### 与 fallback 的交互

当模型配置了 fallback 时，每个候选会**独立**限流：

```yaml
model_list:
  - model_name: gpt4-with-fallback
    provider: openai
    model: gpt-4o
    rpm: 5
    fallbacks:
      - gpt-4o-mini   # must also be in model_list; its own rpm applies
```

如果当前候选的桶为空且还有更多可用候选，GoGoClaw 会跳过本地已饱和的候选，立刻尝试下一个 fallback。只有最后一个剩余候选会等待令牌补充。如果在等待最后一个候选时命中了上下文截止时间，等待错误会向上传播。

对于解析到相同底层 Provider/模型 的 `model_list` 别名，限流以稳定的配置身份（例如 `model_name`）为键，而不是解析后的运行时模型字符串。这样可以为多密钥和基于别名的配置保留不同的 RPM 设置。

### 突发行为

桶初始为**满**（突发 = RPM）。对于 `rpm: 3`，前 3 个请求会立即发出；后续请求间隔约 20 秒。

对于严格的 API，若想降低突发性，请设置较低的 `rpm` 并依赖稳态补充。

## 变更的文件

| 文件 | 内容 |
|---|---|
| `pkg/providers/ratelimiter.go` | `RateLimiter`（令牌桶）+ `RateLimiterRegistry` |
| `pkg/providers/ratelimiter_test.go` | 限流器和注册表的单元测试 |
| `pkg/providers/fallback.go` | `FallbackCandidate.RPM` 字段；`FallbackChain.rl`；`Execute`/`ExecuteImage` 中的 `Wait()` 调用 |
| `pkg/agent/model_resolution.go` | 从 `model_list` 解析候选，保留稳定的配置身份并将 `RPM` 传播到 `FallbackCandidate` |
| `pkg/agent/loop.go` | 构建 `RateLimiterRegistry`，注册所有 agent 的候选，并传给 `NewFallbackChain` |
