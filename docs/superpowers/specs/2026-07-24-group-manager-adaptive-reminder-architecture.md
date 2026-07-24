# Group-manager adaptive Reminder architecture

Status: implementation contract for task #680
Date: 2026-07-24
Related designs:

- `2026-07-08-agent-reminders-design.md`
- `2026-07-14-beckham-group-manager-design.md`
- `2026-07-14-wendy-work-graph-supervisor-design.md`
- `2026-07-22-raft-reminder-parity.md`

## 1. Decision

The group manager uses Reminder for one purpose only: adaptive patrol of a
managed group.

Issue creation, assignment, and status changes already have a platform delivery
path. They create issue system events, target the relevant group member, and
enqueue or wake the assigned agent where applicable. That delivery boundary is
the source of truth. The workgraph and group manager must not create a second
`start_work`, `unlock`, progress-nudge, interrupt, or route-change wake.

The manager remains an ordinary group member with additional judgment context.
A patrol wake asks it to inspect the current group state and decide whether
speaking is useful. It does not mechanically narrate workgraph transitions.

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

The initial fire is 15 minutes after creation.

### 5.2 Fire and fallback

At fire time the server transactionally revalidates:

- the group is still live and still bound to the same manager;
- the manager is live and still a group member;
- the manager has a usable runtime;
- the Reminder is `group_manager_auto/patrol`.

Before waking the manager, the same Reminder definition is re-armed for
`now + 24h`. This is the failure fallback: a crashed, rate-limited, or silent
manager cannot permanently break patrol.

The private patrol prompt asks the manager to inspect current messages, issues,
tasks, ownership, commitments, and progress. It must:

- say nothing when no coordination is useful;
- speak as a normal group member only when context merits it;
- avoid repeating platform system events or changing assignee priority;
- replan the same Reminder definition between 15 minutes and 24 hours.

Replanning uses the existing versioned Reminder mutation boundary. It does not
insert another patrol row.

## 6. Human surface and permissions

The Reminder list shows both ordinary reminders and the active patrol. Patrol
rows show:

- source badge `Automatic · Group management`;
- next fire time;
- last fire time when available;
- the managed group anchor.

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
| Whether group-level coordination is useful | manager judgment during patrol |
| Future patrol time | Reminder definition |

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
5. patrol fire/replan/fallback, cancellation, re-enable, permissions, History
   exclusion, and one-active-row uniqueness pass against PostgreSQL;
6. frontend list and action tests expose patrol plus user reminders only;
7. exact-head CI, migration deploy, served API/UI, and daemon capability gates
   pass before live closure.
