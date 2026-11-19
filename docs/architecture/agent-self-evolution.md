# Agent Self-Evolution

GoGoClaw can turn recurring task evidence into workspace skill drafts. Self-evolution is disabled by default and is configured under `evolution`.

## Modes

| Mode | Behavior | Use case |
| --- | --- | --- |
| `observe` | Records turn outcomes and skill usage. | Collect examples before spending model calls on drafts. |
| `draft` | Records evidence, clusters tasks, and prepares candidates for review. | Evaluate proposed changes while keeping the live skills stable. |
| `apply` | Generates the same candidates and enables publication after explicit approval in the console. | Publish a reviewed improvement to the active workspace. |

## Learning and drafting

The background recorder saves a short goal and output excerpt, the turn status, used skills, and tool names and outcomes. Heartbeat turns are ignored. Drafting evaluates eligible records in batches. A finished turn supplies an execution status. A separate model decision determines whether it achieved the goal. Decision results are cached, a failed judge request leaves the result unknown, and failed turns can count against a cluster even when they produced no final text.

Related cases have to meet the configured evidence count and success threshold. New failures update the success rate of matching patterns. Changes to the evidence block publication of older candidates. Once a pattern meets the threshold, a valid new revision supersedes the earlier candidate and retains its history.

`after_turn` schedules drafting after a turn, `scheduled` uses the configured local `HH:MM` times, and `manual` waits for a user request. In Draft or Apply mode, open **Settings → Evolution → Evolution drafts** and select **Generate drafts** to run it on demand.

## Review and recovery

The console shows evidence, the rendered `SKILL.md` diff, and any name collision with a global or built-in skill. Generated instructions become part of the agent's future context after publication, so review them before approving the draft. The local scan checks document structure and familiar secret patterns.

Apply mode requires explicit approval for each draft. Approval binds the reviewed content to the target version, and the file is checked again before replacement. If the target has changed, refresh the preview to review against the updated skill. In Draft and Apply modes, candidates can be rejected.

Publication keeps a backup of the previous file. **Roll back version** restores that file, or removes the file created by the selected publication. Rollback checks for later changes and remains available when evolution is disabled. For versions published before this workflow, restore the legacy backup manually. Idle generated skills can be listed as archive suggestions. Evolution leaves those skills on disk until you take action.

## Storage and upgrades

Records, patterns, drafts, and profiles are stored in the workspace's `sessions/sessions.db`. At first use, GoGoClaw imports legacy JSONL records, draft JSON, and profile JSON from the configured evolution directory in a single transaction. The originals stay in place, and existing SQLite entries take precedence.

`state_dir` locates the legacy input and the `backups` directory. It defaults to `<workspace>/state/evolution`. Include the database and the backup directory in workspace backups if you want to retain rollback support.

See the [Configuration Guide](../guides/configuration.md#agent-self-evolution) for the available fields.
