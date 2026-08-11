# Agent Reminders — Design (V1)

- Date: 2026-07-08
- Source: PRD `docs/product-conversation-model-prd.md` §6.4 (时间驱动唤醒 / Reminder, P2 候选, Frank 已点头方向; 2026-07-08 Frank 批准实施)
- Raft parity reference: `raft reminder schedule/list/snooze/update/cancel` (observed on raft-daemon 0.71.0)

> Historical baseline only. The active-count cap, visible fire receipt, durable
> owner queue, and other superseded delivery semantics in this V1 document are
> replaced by `2026-07-22-raft-reminder-parity.md` and ADRs 0014, 0016, and 0018.

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
   Visibility and wake semantics of this row are governed by the system-message contract below.
2. **Wake**: `ensureChannelAgentSession` → `createChannelAgentPromptMessage` with a reminder
   prompt (title + anchor message excerpt + "reply in this surface") →
   `TaskService.EnqueueChatTask` (**priority 2** → `Directed=true` → must-reply brief renders).
   Mute does NOT suppress a reminder fire (direct-level, wake ownership is the author's own
   request). Agent offline → the queued task waits for daemon delivery (existing durable-queue
   behavior satisfies "到点时 agent 离线 → 入队待上线").

## System-message contract (#329 alignment — 一条数据,两类读者)

The fire receipt rides the #329 decision: system notices reuse the message flow as
`author_type='system'` rows — **not** a separate UI notification layer. One row, two audiences:

1. **Humans see it in the surface — already true today (verified).** The main-timeline,
   thread, sidebar-preview, and unread-count queries carry no `author_type <> 'system'`
   filter. The filters that do exist are interaction guards and stay as-is: search excludes
   system rows (`channel.go:1041`, `agent_transport.go:642`), system rows cannot be
   quote-replied (`channel.go:2135`) or reacted to (`channel.go:1770`). No change needed.
2. **Agents see it in bundles, marked as system — currently FALSE, fix in scope.** The
   ambient unread-bundle query excludes system rows (`channel_ambient_wake.go:193
   author_type <> 'system'`), so agents today cannot read system notices at all —
   inconsistent with raft (agents receive `type=system` lines, informational, usually no
   reply) and with #329's "一条数据,两类读者". Fix: include system rows in unread bundles,
   rendered with an explicit system marker in the bundle line format; keep the existing
   guidance that system lines rarely need replies. This fix benefits all system notices
   (welcome lines, future unfollow notices), not just reminder receipts.
3. **System rows never wake — holds by construction, add a regression guard.** Wake dispatch
   is caller-driven (`dispatchChannelMessageToAgents` runs only where user/agent sends call
   it); the fire path simply never calls dispatch for the receipt. Same family as
   "reaction 不唤醒" and "edit/delete 绝不产生新 wake" — otherwise every receipt becomes an
   N-agent ambient amplifier violating the §6.3 bounded-load invariant. Add a test asserting
   a receipt insert produces zero new tasks and no ambient `pending_wake` bump.

### Broadcast boundary (Parker's rule: 影响别人预期→公示,只关自己→静默)

| Lifecycle event | Broadcast? | Rationale |
|---|---|---|
| schedule / snooze / update / cancel | **No system message.** Queryable via `reminder list` + Activity events. | 只关作者自己的注意力管理 — same boundary as channel mute (#329: mute 静默、可查询不广播). Raft's schedule-receipt is opt-in (`--channel`); V1 omits it. |
| fire | **System line in the anchored surface.** | 影响该 surface 参与者的预期(作者要回来跟进了)— same boundary as thread unfollow 公示. |

Wiring: the fire path needs handler-package helpers, so the fire executor is a
`*handler.Handler` scanner method (removed by the V3 daemon-timer cutover); preferred wiring was exposing the handler from
router construction (add a variant of `NewRouterWithOptions` returning it, or an accessor) so
`main.go` can hand it to the scheduler goroutine — implementer picks the least invasive form.

## API (agent transport auth)

Flat routes beside `/api/agent/messages/*` (router.go ~:1074), all POST, all resolved through
`requireAgentTransportTask` (author = task's agent; workspace from token):

| Route | Body | Notes |
|---|---|---|
| `/api/agent/reminders/schedule` | `{title, delay_seconds? \| fire_at?, message_id}` | `delay_seconds` preferred (server clock, tz-safe); `fire_at` ISO-8601 UTC. `message_id` is required and must resolve to a readable channel message or thread; the handler never infers it from task prompt text. Cap: max **25** `scheduled` per (workspace, agent) → 409 coded `reminder_cap_exceeded`. Delay bounds: 60s … 90d. |
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

## Acceptance (from PRD §6.4 + #329 system-message contract)

1. schedule → 到点唤醒 author agent 一次 directed run,run 里可见 reminder 上下文(title+锚点)。
2. snooze / update / cancel 生效;list 可查。
3. 到点时 agent 离线 → 入队待上线(durable queue 既有行为)。
4. 重复 fire 幂等(claim 原子性 + 单行状态机)。
5. per-agent 数量上限显式报错(409 coded)。
6. fire 回执出现在锚定 surface 的**人类可见时间线**(thread 锚定时出现在 thread 内)。
7. 回执以 `type=system` 进入其他 agent 的 unread bundle(可读、通常不回)。
8. 回执**不触发任何唤醒**(directed/ambient 均不触发;ambient 高频 fire 下 run 数不随回执数增长)。
9. schedule/snooze/update/cancel 无广播消息(静默,`reminder list` + Activity 可查)。
10. 生命周期事件进 agent_activity_event(fail-soft)。
