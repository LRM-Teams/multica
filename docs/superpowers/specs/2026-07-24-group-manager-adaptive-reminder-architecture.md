# Group-manager issue-progress Reminder architecture

Status: implementation contract for tasks #680, #698, and #703
Date: 2026-07-24
Related designs:

- `2026-07-08-agent-reminders-design.md`
- `2026-07-14-beckham-group-manager-design.md`
- `2026-07-14-wendy-work-graph-supervisor-design.md`
- `2026-07-22-raft-reminder-parity.md`

## 1. Decision

The group manager uses Reminder for one purpose only: issue-progress patrol of
a managed group.

Issue creation, assignment, and status changes already have a platform delivery
path. They create issue system events, target the relevant group member, and
enqueue or wake the assigned agent where applicable. That delivery boundary is
the source of truth. The workgraph and group manager must not create a second
`start_work`, `unlock`, progress-nudge, interrupt, or route-change wake.

The manager remains an ordinary group member with additional judgment context.
A patrol wake asks it to inspect active issue state, decide whether speaking is
useful, and choose the next check from a bounded set. It does not mechanically
narrate workgraph transitions.

Ordinary group quietness or chatter is not a progress signal. Progress is
derived from issue status/assignment, issue comments, issue-linked task
lifecycle, and issue-source scope changes. The server owns the unique durable
Reminder and its safety policy; the manager owns the contextual choice of the
next check within that policy.

This is a cutover, not a compatibility layer. Migration 221 removes the legacy
`pending_handoff` queue. Task #671 D6 separately removes the residual ambient
scheduler path; that deletion is not part of Reminder execution.

## 2. Identity and authority

A managed group is expressed only by:

- `channel.kind = 'group'`
- `channel.group_manager_agent_id`
- `agent.managed_role = 'group_manager'`

Names such as Beckham or Wendy never establish authority. The manager must be
live, belong to the same workspace, and be an agent member of the group.

The runtime must advertise `reminder_versioned_cache_v1`. Managed groups whose
bound manager lacks that capability fail explicitly when the patrol is created
or re-enabled. Ordinary groups without a manager remain unaffected.

## 3. Data contract

Migration 221 adds explicit provenance to `agent_reminder`:

```text
origin_kind   agent | group_manager_auto
managed_kind  null | patrol
origin_key    null | stable patrol key
```

Checks enforce exactly two shapes:

- ordinary reminder: `origin_kind='agent'`, with no managed kind or key;
- managed patrol: `origin_kind='group_manager_auto'`,
  `managed_kind='patrol'`, and a non-empty origin key.

There is no handoff payload or handoff Reminder kind. One partial unique index
allows at most one scheduled or firing patrol per
`(workspace_id, agent_id, anchor_channel_id)`.

`fire_at` is the next patrol time and `fired_at` is the last patrol time.
Occurrences and lifecycle events remain an internal diagnostic ledger. Managed
patrol occurrences are excluded from human Reminder History.

Migration 222 adds:

```text
managed_backoff_step  0 | 1 | 2 | 3
```

The four steps map exactly to 15, 30, 45, and 60 minutes. They are a bounded
controlled choice and fallback ledger, not a free-form recurrence.

## 4. Cutover

Migration 221:

1. refuses to proceed while any legacy `pending_handoff` row is `claimed`,
   because an old scheduler may still own it;
2. intentionally discards unclaimed legacy handoff rows instead of converting
   them into a second delivery mechanism;
3. backfills one patrol for each live managed group;
4. installs lifecycle triggers for manager membership and group binding;
5. drops `pending_handoff`.

The migration must run only after the old handoff scheduler has drained. The
down migration recreates an empty legacy table for schema rollback; it cannot
reconstruct discarded coordination rows and must not invent them.

## 5. Patrol lifecycle

### 5.1 Bootstrap

Migration 221 backfills one patrol per live managed group. Binding a manager
later invokes `ensureGroupManagerPatrolIfNeverCreated`.

Bootstrap checks all historical patrol rows, not just active rows. A cancelled
patrol therefore stays cancelled across rebind and restart. Only an authorized
Reminder action may re-enable it.

Bootstrap resolves the managed group's active issue scope:

- an issue is active unless its status is `done` or `cancelled`;
- an issue is in scope when it shares the group's `project_id` or has an
  `issue_source_message` anchor in that group.

If active issues exist, the initial fire is 15 minutes after creation. If none
exist, the durable Reminder definition starts in dormant `fired` state and
creates no agent wake.

### 5.2 Progress reset and dormancy

Migration 222 installs database triggers for:

- issue creation and changes to status, assignee, or project;
- issue comment creation/content update;
- issue-linked task creation/status update;
- issue-source-message and channel-project scope changes.

These triggers update the existing patrol definition; they never create a
second patrol. Any real in-scope progress schedules the non-cancelled patrol for
15 minutes and resets `managed_backoff_step` to `0`. When the last active issue
leaves scope or becomes terminal, a scheduled patrol becomes dormant `fired`.
A later real issue event re-arms a dormant `fired` patrol. An explicitly
cancelled patrol remains cancelled.

There is intentionally no trigger on ordinary channel messages.

### 5.3 Fire, manager choice, and fallback

At fire time the server transactionally revalidates:

- the group is still live and still bound to the same manager;
- the manager is live and still a group member;
- the manager has a usable runtime;
- the Reminder is `group_manager_auto/patrol`.

The server then classifies the in-scope active issues:

- `backlog` or `todo`: pending ownership/start;
- `in_progress`: executing;
- `in_review`: reviewer or merge gate;
- `blocked`: blocker follow-up.

If no active issue remains, the occurrence is cancelled as
`patrol_no_active_issue_dormant`, the definition becomes dormant `fired`, and no
agent task is created.

Before waking the manager for active work, the same Reminder definition is
re-armed with a bounded automatic fallback. The fallback advances through
15/30/45/60 minutes using `managed_backoff_step`; it stays at 60 minutes after
the one-hour boundary. If any active issue is blocked, the fallback is always
15 minutes. A crashed, rate-limited, or silent manager therefore cannot
permanently break patrol or move it beyond one hour.

The private patrol prompt asks the manager to inspect active issue/task state,
ownership, review gates, blockers, concrete outputs, and explicit near-term
commitments. It must:

- say nothing when no coordination is useful;
- speak as a normal group member only when context merits it;
- avoid repeating platform system events or changing assignee priority;
- choose the next check for the same Reminder with exactly one of
  `delay_seconds=900|1800|2700|3600`;
- use 900 seconds whenever any issue is blocked;
- never create, cancel, or mutate another patrol Reminder.

The choice uses the existing authenticated, versioned Reminder mutation
boundary. The server rejects free-form times, choices without an active issue,
and blocked-work choices above 15 minutes; successful choices persist the
matching `managed_backoff_step` and an auditable lifecycle reason. If the
manager does not choose, the pre-armed fallback remains authoritative.

## 6. Human surface and permissions

The Reminder list shows both ordinary reminders and the managed patrol,
including a dormant patrol. A dormant row remains visible so it cannot be
confused with a group that has no manager configured.

Patrol rows show:

- source badge `Automatic · Group management`;
- active: the real next fire time;
- dormant: no `next_fire_at` and a localized `Dormant` status badge;
- last fire time when available in both states;
- the managed group anchor.

The server returns dormant `group_manager_auto/patrol` definitions from the
definitions projection with `status='fired'`, preserves `last_fire_at`, and
omits `next_fire_at`. The same delivery includes the frontend Zod schema,
row-level adapter, localized badge, and list rendering for that exact shape, so
the dormant projection is accepted without a false countdown before migration
222/server deployment.

The list has no handoff row, handoff badge, workgraph reason, or next-work
projection.

Workspace owners/admins and the bound group manager may manage the patrol.
Another ordinary member or agent may not. Cancel is durable. Snooze and
reschedule operate on the same row with compare-and-swap versioning; re-enable
creates the authorized scheduled successor without allowing two active patrols.

## 7. Source-of-truth boundary

The following must remain separate:

| Concern | Owner |
|---|---|
| Issue creation/assignment/status visibility | issue system events |
| Directed assignee delivery and wake | issue/task delivery path |
| Dependency and progress state | workgraph |
| Active issue scope and progress reset | server issue/comment/task triggers |
| Whether group-level coordination is useful | manager judgment during patrol |
| Next-check choice | manager, one of 15/30/45/60 minutes |
| Dormancy, blocked cap, fallback, uniqueness, and persistence | server Reminder state machine |

The workgraph may update its own node and edge state after issue changes. It may
not translate that state into manager Reminder handoffs or priority-changing
wakes.

## 8. Required verification

The release gate requires:

1. migration up proves claimed legacy rows fail closed, unclaimed rows are
   discarded, one patrol is backfilled, lifecycle teardown cancels it, and the
   old table is dropped;
2. migration down recreates an empty legacy table and removes managed Reminder
   provenance;
3. issue backflow proves target agents receive directed system-event sessions,
   unrelated agents receive none, and the same flow creates zero managed
   Reminders;
4. source search proves no workgraph detector or message-signal path creates a
   managed handoff;
5. migration 222 proves active issue scope, no-active dormancy, idempotent
   cutover, and comment/task/status progress reset to 15 minutes;
6. patrol fire proves no-active creates no agent task, active fire creates one
   task plus a bounded fallback, blocked fallback stays at 15 minutes, and the
   prompt carries the four issue classes;
7. controlled replan accepts only 900/1800/2700/3600, persists the matching
   step, rejects free-form times, rejects no-active choices, and rejects blocked
   choices above 900;
8. cancellation, re-enable, permissions, History exclusion, and one-active-row
   uniqueness pass against PostgreSQL;
9. frontend Zod fallback, row adapter, and list tests preserve a dormant patrol
   with no `next_fire_at`, render its last patrol plus localized Dormant badge,
   and expose patrol plus user reminders only;
10. exact-head CI, migration deploy, served API/UI, and daemon capability gates
   pass before live closure.
