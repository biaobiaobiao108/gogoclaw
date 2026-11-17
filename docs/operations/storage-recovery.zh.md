# SQLite 存储恢复

GoGoClaw 把会话事实和 Seahorse 检索索引保存在 `sessions/sessions.db`。运行时启动会执行 `PRAGMA quick_check(1)`，数据库损坏时拒绝打开；Schema 初始化在事务中完成。

## 命令

`storage` 通过 `--db` 定位数据库，或通过 `--workspace` 加上 `sessions/sessions.db`；两者都不提供时使用配置中的默认工作区。

```bash
gogoclaw storage check --workspace ~/gogoclaw-workspace
gogoclaw storage check --db ~/gogoclaw-workspace/sessions/sessions.db --full
gogoclaw storage backup --workspace ~/gogoclaw-workspace
gogoclaw storage backup --db ~/gogoclaw-workspace/sessions/sessions.db -o /safe/backups/sessions.db
gogoclaw storage restore /safe/backups/sessions.db --workspace ~/gogoclaw-workspace --force
```

`check` 执行 `quick_check`、`foreign_key_check`，并报告数据库/WAL 大小与耗时；`--full` 使用较慢的 `integrity_check`。

`backup` 使用 `VACUUM INTO`，先检查源库，结果权限设为 `0600`，随后再次校验备份。它拒绝覆盖已存在的目标文件；未提供 `--output` 时，会在源库旁生成带时间戳的 `.backup-*` 文件。

`restore` 会验证源文件与暂存副本，为已有目标创建安全备份，清理旧的 `-wal`/`-shm` 文件，再通过原子重命名替换目标。输出中会列出安全备份路径。

## 运维规则

- 恢复前停止 gateway；替换期间不要让另一个进程使用同一数据目录。
- 保留安全备份，直到恢复后的实例通过启动和读取检查。
- 将备份放在工作区之外；包含敏感会话数据时应加密保存。
- `VACUUM INTO` 可以在线生成一致快照，但静默实例能提供最清晰的运维证据。
- 容器仍然是隔离边界。完整性检查和备份是恢复工具，不能替代文件权限或容器限制。
