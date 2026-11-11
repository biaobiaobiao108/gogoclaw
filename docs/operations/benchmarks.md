# Reproducible Performance Benchmarks

The repository provides `make benchmark` to generate comparable reports using a fixed benchmark set, repetition count, and runtime metadata. It covers Agent message construction, JSONL session storage, Seahorse memory retrieval, and BM25 search.

```bash
make benchmark
```

By default, each benchmark runs for 2 seconds and is repeated 3 times, with reports written to `build/benchmarks/`. These can be adjusted through environment variables:

```bash
GOGOCLAW_BENCH_TIME=5s GOGOCLAW_BENCH_COUNT=5 make benchmark
```

You can also specify the output path or build tags:

```bash
GOGOCLAW_BENCH_OUTPUT=/tmp/gogoclaw-bench.txt make benchmark
GOGOCLAW_BENCH_TAGS=goolm,stdjson make benchmark
```

The script records the UTC time, Git commit, Go version, operating system, CPU information, logical core count, GOMAXPROCS, tags, and benchmark parameters. When comparing two results, try to use the same machine, Go version, power mode, container limits, and load; do not treat numbers from a GitHub-hosted runner directly as performance data for a NAS.

## Measuring on a NAS

To evaluate low-power devices such as RK3566, build and run a report for the same commit on the target device:

```bash
git rev-parse HEAD
go version
make benchmark
```

Record the device model, system version, container CPU/memory limits, and storage medium, and repeat runs while the device is idle. Startup latency should be recorded separately with multiple cold-start and warm-start measurements; this benchmark report does not disguise startup latency as a Go microbenchmark.

## GitHub Actions

The repository's manual `Benchmark` workflow generates and retains report artifacts on a fixed Ubuntu runner. It is suitable for comparing trends in code changes on the same class of runner, but cannot replace measurements on the target NAS.
