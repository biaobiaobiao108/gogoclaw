# Reliability checks

The deterministic multi-session restart test can be extended without changing CI defaults:

```sh
GOGOCLAW_HARNESS_RESTART_ROUNDS=50 go test -tags goolm,stdjson ./pkg/harness -run '^TestProductFlowConcurrentSessionsSurviveRestarts$' -count=1 -v
```

Each round runs eight concurrent sessions against SQLite, closes the store, and reopens it. The final check reads the complete transcript, including messages removed from the model context by compaction. Progress logs include elapsed time, goroutine count, and Go heap allocation. Adjust the round count and Go test timeout for longer runs. This is a deterministic in-process test; it does not measure container RSS or prove real model and channel recovery.

The Reliability stress workflow runs 50 rounds weekly and can also be started manually. Its log is retained as an artifact for 30 days. The Benchmark workflow also runs weekly and retains its environment and results; compare reports from the same runner and configuration before drawing conclusions about regressions.

The standalone gateway Compose health check uses `/ready`, which indicates that gateway startup completed. `/health` only indicates that the HTTP process responds. The launcher Compose check uses its own `/health` endpoint, including before the first model is configured. Authenticated monitoring can query `/api/gateway/readiness` on the launcher to get the gateway's readiness status. Neither check proves that a model or external channel is reachable; inspect the dashboard and send a test message to verify those dependencies.

`TestStoppedDataBackupRestoresSessionAndWorkspace` exercises a stopped-store copy into a fresh directory, checking the transcript, attachment, workspace file, scheduled job, model configuration and encrypted credential with its test key. For deployment backups follow the stopped-container procedure in [Docker deployment](../guides/docker.md); the test does not replace an actual container restore drill or test a key stored outside the data directory.

The CI browser job also starts a real launcher with a temporary data directory and checks first-time setup, rejected and successful login, model persistence, and the approval setting. The model is created through the real API and verified in the UI after reload; the approval switch is changed in the UI and checked against the persisted custom policy. The browser test also starts the gateway with a loopback model fixture, sends a message, reloads the transcript, and verifies that the input and reply are absent from the launcher log output.

The central application logger masks common credential formats and sensitive structured field names. This is best effort: unknown credential formats, standard-library and other programs' logs, and data without a recognizable label may still contain secrets. Limit log access and review diagnostic output before sharing it.

## Text delivery and logs

Telegram and Weixin text replies, scheduled output, and text from the `message` tool record recent delivery outcomes in the session. View or refresh those outcomes in Session Management. A delivery is marked accepted after the platform returns success. A timeout, interruption, or partial send is marked unknown. After restart, only queued deliveries that never started are offered again. The worker limits retries to errors the channel classifies as safe to retry.

Standard debug logs include diagnostics such as turn IDs, model and tool names, and message lengths. To collect request or reply previews, use DEBUG and set `GOGOCLAW_CONTENT_DIAGNOSTICS_UNTIL` in the gateway environment before startup to a future RFC 3339 timestamp. After the deadline, new previews stop. Previously written logs stay subject to the usual retention settings.

## Container upgrades and session pagination

Run the restore drill in an isolated Docker environment. An optional baseline image exercises upgrade and rollback using separate data volumes.

```sh
GOGOCLAW_TEST_BASELINE_IMAGE=gogoclaw:previous GOGOCLAW_TEST_IMAGE=gogoclaw:candidate bash scripts/test-container-restart.sh
```

Compare first-page P95 for the SQLite and compatibility paths against the same fixture of 300 sessions. The cold benchmark creates a fresh handler for each sample. The operating system may still cache files.

```sh
go test -tags goolm,stdjson ./web/backend/api -run '^$' -bench '^BenchmarkSQLiteSessionListPage$' -benchtime=25x -count=1
```
