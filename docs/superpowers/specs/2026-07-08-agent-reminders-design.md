# Agent Reminders — Design (V1)

- Date: 2026-07-08
- Source: PRD `docs/product-conversation-model-prd.md` §6.4 (时间驱动唤醒 / Reminder, P2 候选, Frank 已点头方向; 2026-07-08 Frank 批准实施)
- Raft parity reference: `raft reminder schedule/list/snooze/update/cancel` (observed on raft-daemon 0.71.0)

## Problem

The Wake matrix (§6) is entirely message-driven — an agent cannot "follow up at a future time"
(wait for CI, nudge someone tomorrow morning). Today's two bad options: keep the run alive
(wastes runtime) or write a memory note (unreliable — needs a future wake to be read).
Runtime-native cron/wakeup tools are the wrong answer: they are invisible to the platform
(no backpressure accounting, no Activity trail, no per-agent caps, lost on daemon restart).
Raft ships reminders as the platform primitive and blocklists runtime scheduling tools.

## Model

Reminder = **author-owned, persistent, observable, snoozable, cancelable wake signal**,
anchored to a channel message/thread. When it fires, it wakes the author agent (one directed
run) — wake ownership never transfers. The fire receipt is visible in the anchored surface
as a system message.

### V1 scope

In: one-shot reminders, agent authors only, anchor required, schedule/list/snooze/update/cancel,
per-agent active cap, fire → system receipt + directed run, offline-agent durability (queue
holds the run until the daemon delivers), lifecycle events into `agent_activity_event`.

Out (deliberate): recurrence (`--repeat`, raft has it, PRD V1 does not require it), human
authors / any UI surface, a dedicated `reminder log` command (lifecycle is queryable via the
existing activity feed), runtime disallowed-tools enforcement (follow-up alongside daemon work).

## Schema (migration 154)

```sql
CREATE TABLE reminder (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 500),
  anchor_channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  anchor_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  anchor_thread_root_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  fire_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'scheduled'
    CHECK (status IN ('scheduled', 'firing', 'fired', 'cancelled')),
  fired_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
  snooze_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  fired_at TIMESTAMPTZ
);
CREATE INDEX idx_reminder_due ON reminder(fire_at) WHERE status = 'scheduled';
CREATE INDEX idx_reminder_agent_active ON reminder(workspace_id, agent_id) WHERE status = 'scheduled';
```

Notes:
- `anchor_message_id` is **required at creation** (agent-authored reminders must anchor, raft
  parity); nullable in the schema only so message deletion doesn't cascade away the reminder
  (`ON DELETE SET NULL` — the reminder still fires, receipt goes to the channel/thread).
- `anchor_thread_root_message_id` is resolved server-side from the anchor message (its
  `thread_root_message_id`, or itself when the anchor is a thread root with replies — keep it
  simple: copy the anchor's own `thread_root_message_id`; receipt and wake target the thread
  when set, else the main timeline).
- No recurrence column in V1 — additive later.

## Firing (scheduler)

New goroutine `runReminderScheduler` in `server/cmd/server/`, modeled on
`autopilot_scheduler.go` (30s tick). Claim is an atomic status transition
(same family as autopilot's claim-by-nulling):

```sql
UPDATE reminder SET status = 'firing', updated_at = now()
WHERE status = 'scheduled' AND fire_at <= now()
RETURNING *;
```

Per claimed row, fire; on success `status='fired', fired_at=now(), fired_task_id=$task`.
On failure log + `status='scheduled'` retained? No — mark back to `scheduled` only for
transient errors; if the channel/agent is gone, `status='cancelled'` + activity event
(`reason_code='anchor_gone'`). Startup recovery (like `recoverLostTriggers`): rows stuck in
`firing` with `updated_at < now() - 5 min` and `fired_task_id IS NULL` → back to `scheduled`.

### Fire = receipt + directed wake

1. **Receipt**: insert a system message into the anchored surface via
   `insertChannelMessageWithParts(..., authorType="system")` — content
   `⏰ Reminder for @<agent>: <title>`, threaded when `anchor_thread_root_message_id` is set.
   ⚠ Implementation must verify system messages are visible on the main timeline read path
   (some read paths filter `author_type <> 'system'` — check `channel.go:1041`-style filters;
   if the timeline hides system messages, surface the receipt as the wake prompt context only
   and record the gap in the PR).
2. **Wake**: `ensureChannelAgentSession` → `createChannelAgentPromptMessage` with a reminder
   prompt (title + anchor message excerpt + "reply in this surface") →
   `TaskService.EnqueueChatTask` (**priority 2** → `Directed=true` → must-reply brief renders).
   Mute does NOT suppress a reminder fire (direct-level, wake ownership is the author's own
   request). Agent offline → the queued task waits for daemon delivery (existing durable-queue
   behavior satisfies "到点时 agent 离线 → 入队待上线").

Wiring: the fire path needs handler-package helpers, so the fire executor is a
`*handler.Handler` method (`FireDueReminders`); preferred wiring is exposing the handler from
router construction (add a variant of `NewRouterWithOptions` returning it, or an accessor) so
`main.go` can hand it to the scheduler goroutine — implementer picks the least invasive form.

## API (agent transport auth)

Flat routes beside `/api/agent/messages/*` (router.go ~:1074), all POST, all resolved through
`requireAgentTransportTask` (author = task's agent; workspace from token):

| Route | Body | Notes |
|---|---|---|
| `/api/agent/reminders/schedule` | `{title, delay_seconds? \| fire_at?, message_id?}` | `delay_seconds` preferred (server clock, tz-safe); `fire_at` ISO-8601 UTC. `message_id` omitted → the task's trigger message when resolvable, else 400. Cap: max **25** `scheduled` per (workspace, agent) → 409 coded `reminder_cap_exceeded`. Delay bounds: 60s … 90d. |
| `/api/agent/reminders/list` | `{status?}` | Own reminders only; default `scheduled`+`firing`. |
| `/api/agent/reminders/snooze` | `{id, delay_seconds \| fire_at}` | `scheduled` or `fired` → back to `scheduled`, `snooze_count+1`. |
| `/api/agent/reminders/update` | `{id, title? , delay_seconds? \| fire_at?}` | `scheduled` only. |
| `/api/agent/reminders/cancel` | `{id}` | `scheduled` only → `cancelled`. |

`id` accepts full UUID or unique 8-char prefix within (workspace, agent) — raft parity;
ambiguous/unknown prefix → 404/409 coded. Every state change emits `recordAgentActivityEvent`
(`event_kind='lifecycle'`, `event_type='reminder_scheduled'|'reminder_snoozed'|'reminder_updated'|
'reminder_cancelled'|'reminder_fired'`, `target_kind='channel'|'thread'`, fail-soft).

## CLI

`multica reminder` (groupCore), children mirroring raft:

```
multica reminder schedule --title "..." (--delay-seconds N | --fire-at ISO) [--message-id <id>] [--output json]
multica reminder list [--status scheduled|fired|cancelled|all]
multica reminder snooze --id <uuid-or-prefix> (--delay-seconds N | --fire-at ISO)
multica reminder update --id <id> [--title ...] [--delay-seconds N | --fire-at ISO]
multica reminder cancel --id <id>
```

Pattern: `cmd_channel.go` (newAPIClient → cli.APIContext → PostJSON). No top-level alias needed
(new command, canonical from day one).

## Runtime brief

`runtime_config.go` Available Commands (CLI-transport-available branch) gains:

> - Reminders: schedule a future self-wake with `multica reminder schedule --title "..."
>   --delay-seconds N [--message-id <id>]` when follow-up depends on future state (waiting on
>   CI, nudging tomorrow). Prefer platform reminders over runtime cron/sleep — they survive
>   restarts, are visible to your team, and wake you with the anchored context. Use
>   `multica reminder snooze/update/cancel` to manage them; do not busy-wait.

## Acceptance (from PRD §6.4)

1. schedule → 到点唤醒 author agent 一次 directed run,run 里可见 reminder 上下文(title+锚点)。
2. snooze / update / cancel 生效;list 可查。
3. 到点时 agent 离线 → 入队待上线(durable queue 既有行为)。
4. 重复 fire 幂等(claim 原子性 + 单行状态机)。
5. per-agent 数量上限显式报错(409 coded)。
6. anchored surface 出现 fire 回执(或记录可见性 gap)。
7. 生命周期事件进 agent_activity_event。
