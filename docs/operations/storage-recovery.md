# SQLite Storage Recovery

GoGoClaw stores session facts and the Seahorse retrieval index in `sessions/sessions.db`. Runtime startup runs `PRAGMA quick_check(1)` and refuses to open a damaged database. Schema initialization is transactional.

## Commands

The `storage` command locates the database from `--db`, or from `--workspace` plus `sessions/sessions.db`. If neither is supplied, it uses the configured default workspace.

```bash
gogoclaw storage check --workspace ~/gogoclaw-workspace
gogoclaw storage check --db ~/gogoclaw-workspace/sessions/sessions.db --full
gogoclaw storage backup --workspace ~/gogoclaw-workspace
gogoclaw storage backup --db ~/gogoclaw-workspace/sessions/sessions.db -o /safe/backups/sessions.db
gogoclaw storage restore /safe/backups/sessions.db --workspace ~/gogoclaw-workspace --force
```

`check` runs `quick_check`, `foreign_key_check`, and reports the database/WAL size and elapsed time. `--full` uses the slower `integrity_check`.

`backup` uses `VACUUM INTO`, checks the source first, secures the result with `0600`, and validates the copy. It refuses to overwrite an existing destination. If `--output` is omitted, a timestamped `.backup-*` file is created beside the source.

`restore` validates the source and a staged copy. It creates a consistent snapshot of a healthy target. If the target is damaged, it preserves the original database and its SQLite sidecar files before installing the verified replacement. The output includes the preserved file path.

## Operational Rules

- Stop both the gateway and the Web console before restoring. The restore command takes the instance lock. Avoid running other commands that access the database during replacement.
- Keep the safety backup until the restored instance has passed startup and a read check.
- Store backups outside the workspace and encrypt them when they contain sensitive session data.
- `VACUUM INTO` creates a consistent snapshot while the database is live, but a quiet instance gives the clearest operational evidence.
- The container remains the isolation boundary. Integrity checks and backups are recovery tools, not a substitute for filesystem permissions or container limits.
