# 可复现的性能基准

仓库提供 `make benchmark`，用固定的基准集合、重复次数和运行时元数据生成可比较的报告。它覆盖 Agent 消息构建、JSONL 会话存储、Seahorse 记忆检索与 BM25 搜索。

```bash
make benchmark
```

默认每项运行 2 秒、重复 3 次，报告写入 `build/benchmarks/`。可通过环境变量调整：

```bash
GOGOCLAW_BENCH_TIME=5s GOGOCLAW_BENCH_COUNT=5 make benchmark
```

也可以指定输出路径或构建标签：

```bash
GOGOCLAW_BENCH_OUTPUT=/tmp/gogoclaw-bench.txt make benchmark
GOGOCLAW_BENCH_TAGS=goolm,stdjson make benchmark
```

脚本会记录 UTC 时间、Git commit、Go 版本、操作系统、CPU 信息、逻辑核数、GOMAXPROCS、标签和基准参数。比较两份结果时，尽量使用相同的机器、Go 版本、电源模式、容器限制与负载；不要把 GitHub 托管运行器上的数字直接当成 NAS 的性能数据。

## 在 NAS 上测量

要评估 RK3566 等低功耗设备，请在目标设备上构建并运行同一 commit 的报告：

```bash
git rev-parse HEAD
go version
make benchmark
```

记录设备型号、系统版本、容器 CPU/内存限制和存储介质，并在空闲状态下重复运行。启动耗时应另行记录冷启动与热启动的多次测量值；本基准报告不把启动耗时伪装成 Go 微基准。

## GitHub Actions

仓库的手动 `Benchmark` 工作流会在固定的 Ubuntu runner 上生成并保留报告 artifact。它适合比较代码变更在同一类 runner 上的趋势，不能替代目标 NAS 上的实测。
