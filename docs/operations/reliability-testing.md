# Reliability checks

The deterministic multi-session restart test can be extended without changing CI defaults:

```sh
GOGOCLAW_HARNESS_RESTART_ROUNDS=50 go test -tags goolm,stdjson ./pkg/harness -run '^TestProductFlowConcurrentSessionsSurviveRestarts$' -count=1 -v
```

Each round runs eight concurrent sessions against SQLite, closes the store, and reopens it. The final check reads the complete transcript, including messages removed from the model context by compaction. Progress logs include elapsed time, goroutine count, and Go heap allocation. Adjust the round count and Go test timeout for longer runs. This is a deterministic in-process test; it does not measure container RSS or prove real model and channel recovery.

The Reliability stress workflow runs 50 rounds weekly and can also be started manually. Its log is retained as an artifact for 30 days. The Benchmark workflow also runs weekly and retains its environment and results; compare reports from the same runner and configuration before drawing conclusions about regressions.

The standalone gateway Compose health check uses `/ready`, which indicates that gateway startup completed. `/health` only indicates that the HTTP process responds. The launcher check only tests its authentication endpoint. Neither check proves that a model or external channel is reachable; inspect the dashboard and send a test message to verify those dependencies.

`TestStoppedDataBackupRestoresSessionAndWorkspace` exercises a stopped-store copy into a fresh directory. For deployment backups follow the stopped-container procedure in [Docker deployment](../guides/docker.md); the test does not replace an actual container restore drill.

The central application logger masks common credential formats and sensitive structured field names. This is best effort: unknown credential formats, standard-library and other programs' logs, and data without a recognizable label may still contain secrets. Limit log access and review diagnostic output before sharing it.
