# Autopilot full offline — Phase A inventory

Task: #prj-daemon #40  
Date: 2026-08-03  
Owner: Barry

## Goal

Retire Multica autopilot entirely in favor of agent-owned reminders (Frank 2026-07-31). Migrate then delete; no silent stop of active automations.

## Code surface (dev)

| Layer | Path |
|---|---|
| Schema | `autopilot`, `autopilot_trigger`, `autopilot_run` (migration 042+) |
| Service | `server/internal/service/autopilot.go` |
| HTTP | `handler/autopilot.go`, `handler/autopilot_webhook.go` |
| CLI | `server/cmd/multica/cmd_autopilot.go` |
| Daemon | `AutopilotRunID` / `buildAutopilotPrompt` in `prompt.go`, execenv context |
| FE / Desktop | `apps/desktop/.../autopilot-*`, docs `autopilots*.mdx` |

### Trigger kinds

- `schedule` — `cron_expression` + `timezone` + `next_run_at`
- `webhook` — public bearer token, payload on run
- `api` / manual — one-shot fire

### Execution modes

- `create_issue` — materializes issue then agent work
- `run_only` — no issue; closest to reminder wake

## Reminder capability (post #1990/#1994)

- Schedule: `--delay-seconds` | `--fire-at` | `--repeat` (`every:Nh|Nd`, `daily@HH:MM`, `weekly:days@HH:MM`)
- **Required** anchor: `--msg-id` (hard-bound surface)
- ManagerChannels filtered to anchor channel on fire
- No windowed interval (e.g. 09–19 every 2h) → todo **#72**
- No webhook receiver
- No silent create-issue pipeline

## Capability map (product lean, pending Frank)

| Autopilot | Reminder | Decision lean |
|---|---|---|
| schedule/cron | migrate to repeat + msg-id | **migrate** |
| windowed interval | no equivalent | degrade to `every:Nh` or wait **#72** |
| webhook | none | **delete with product** |
| create_issue mode | wake only | **delete silent create**; agent creates if needed |
| concurrency skip/queue/replace | none | **drop** |

## Phase plan

### A — this doc + SQL inventory

```sql
SELECT a.id, a.workspace_id, a.title, a.status, a.execution_mode,
       a.assignee_id, a.created_at
FROM autopilot a
WHERE a.status = 'active'
ORDER BY a.workspace_id, a.created_at;

SELECT t.autopilot_id, t.kind, t.enabled, t.cron_expression, t.timezone, t.next_run_at
FROM autopilot_trigger t
JOIN autopilot a ON a.id = t.autopilot_id
WHERE a.status = 'active';
```

Fill per-row: can migrate Y/N, proposed reminder rule, anchor channel needed.

### B — migrate schedule → reminder

For each migratable row:

1. Ensure assignee agent is channel member (manager if patrol).
2. Pick durable msg-id surface (existing patrol root or post system pin).
3. `multica reminder schedule --title ... --repeat ... --msg-id ...` as that agent.
4. Pause autopilot (`status=paused`) after first successful reminder fire verification.
5. Do **not** delete schema until all active migrated or Frank-approved abandoned.

### C — delete

After zero active (or all paused + Frank GO):

1. Remove HTTP routes, CLI, FE, docs.
2. Migration: drop tables / null FKs on `agent_task_queue.autopilot_run_id`, issue origin.
3. Daemon: remove autopilot prompt path (or leave dead branch until task types gone).
4. Keep separate from squad-leader cleanup (#37).

## Open questions for Frank

1. Webhook-triggered autopilot: OK to remove entirely?
2. Autopilot auto-create issue: OK to remove (agent creates via tools after wake)?

## Non-goals

- #72 windowed interval implementation (separate task)
- #37 squad narrative cleanup
- Touching reminder hard-gate (#1990) behavior

## Status

- 2026-08-03: Phase A mapping posted in #prj-daemon; awaiting prod SQL + Frank product calls.
