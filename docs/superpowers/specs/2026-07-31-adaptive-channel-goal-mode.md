# Adaptive Channel Goal Mode

Status: implemented
Date: 2026-07-31

## 1. Problem

Long-running channel work currently depends on an agent remembering the original
request across messages, wakes, resumed provider sessions, and context
compaction. Issue assignment snapshots preserve issue fields at one wake, but
Multica has no channel-level, server-owned statement of the team's current goal,
success criteria, progress, or blocker.

Making every message a goal would create noise and force project-management
ceremony onto greetings, questions, and small one-step tasks. Goal Mode must
therefore be adaptive: ordinary work remains ordinary, while a channel manager
may create a durable goal when a request needs sustained multi-step or
multi-agent coordination.

## 2. Product decision

1. Goal Mode is off by default.
2. A group channel has at most one current goal.
3. A human channel owner/workspace owner/admin may create, revise, pause,
   resume, complete, or cancel the current goal.
4. An agent with channel role `manager` may create and maintain an agent-authored
   goal through the agent-authenticated boundary. A manager may checkpoint and
   change lifecycle state on a human-authored goal, but the API forbids it from
   rewriting the human-authored title, objective, or success criteria.
5. Ordinary agents do not manage the parent goal. When executing work in a
   channel with an active goal, they receive a compact goal slice on every
   provider turn and may report progress, evidence, a blocker, and the next
   action.
6. Paused, completed, and cancelled goals stop runtime injection immediately.
7. Goal creation does not automatically create Issues in v1. The manager may
   continue using the existing Issue workflow to decompose and assign work.

## 3. Goal lifecycle

```text
active ── pause ──> paused ── resume ──> active
   ├── complete ──> completed
   └── cancel ────> cancelled
```

Creating a new goal is allowed only when no `active` or `paused` goal exists in
the channel. Terminal goals remain available in history.

## 4. Data contract

`channel_goal`:

- identity and scope: `id`, `workspace_id`, `channel_id`;
- intent: `title`, `objective`, ordered `success_criteria`;
- lifecycle: `status`, `version`;
- progress: `progress_summary`, `current_step`, `blocker`, ordered
  `evidence_refs`, `completed_criteria`;
- provenance: creator actor type/id and updater actor type/id;
- timestamps: created, updated, completed.

The database enforces at most one non-terminal goal per channel. Success
criteria and completed criteria are arrays of strings in v1. A completed
criterion must exactly match a current success-criterion string.

## 5. Human API

Channel-member reads:

- `GET /api/channels/{channelId}/goal`

Owner/admin/channel-owner writes:

- `POST /api/channels/{channelId}/goal`
- `PATCH /api/channels/{channelId}/goal`

Creation body:

```json
{
  "title": "Ship Goal Mode MVP",
  "objective": "Make long-running channel work retain its direction",
  "success_criteria": [
    "The channel displays the current goal",
    "Agents receive the active goal on every wake"
  ]
}
```

Patch accepts intent fields, lifecycle status, and progress fields. Updates use
`expected_version`; stale writes return `409`.

## 6. Agent API and CLI

Agent channel members may read the current goal. Only an agent whose
`channel_member.role = 'manager'` may create or revise goal intent. Any channel
agent participating in an active goal may submit a checkpoint:

```json
{
  "progress_summary": "Added the goal read endpoint",
  "current_step": "Wire the Channel card",
  "blocker": "",
  "evidence_refs": ["test:TestGetChannelGoal"],
  "completed_criteria": []
}
```

The CLI surface is:

```text
multica goal get --channel <id-or-name>
multica goal create --channel <id-or-name> ...
multica goal checkpoint --channel <id-or-name> ...
multica goal update --channel <id-or-name> ...
```

The first implementation may land the server boundary and runtime overlay
before all CLI convenience commands, but it must not teach agents a command that
does not exist.

## 7. Runtime contract

The daemon's existing per-wake current-state overlay gains a goal slot. For an
active channel goal it injects:

```text
Current channel goal (server-claimed, authoritative):
- Objective
- Success criteria
- Current step
- Progress
- Blocker
- Goal version
```

The overlay instructs an ordinary executor to advance only its assigned work,
retain the success standard, report evidence/blockers, and not revise the parent
goal. A channel-manager agent additionally receives its existing coordination
duties.

No goal overlay is emitted when the task has no channel, no active goal, or the
goal is paused/terminal. The overlay is per wake rather than startup-only so it
survives resident sessions and compaction.

## 8. Channel UI

Group channels show a compact Goal card directly below the channel header:

- no goal: unauthorized viewers see no persistent card; authorized users receive
  a compact `Set goal` row directly below the header;
- active: title, status, compact progress count, current step, blocker, and an
  expand affordance;
- paused: visible but muted, with Resume for authorized users;
- terminal: removed from the persistent header card;
- expanded: objective, success-criteria checklist, progress summary, evidence,
  and edit/pause/complete/cancel controls.

The card uses React Query server state. Goal mutations are optimistic where
safe and invalidate the channel-goal query on settle. Web and desktop share the
same implementation in `packages/views`.

## 9. Non-goals for v1

- Automatically turning every message or Issue into a Goal.
- LLM keyword scoring as an authoritative creation rule.
- Multiple simultaneous primary goals in one channel.
- A new DAG/project planner.
- Automatic completion based only on agent prose.
- Replacing Issues, reminders, or the group-manager patrol.
- Full goal-history UI.

## 9b. Goal Mode v2 — per-manager process Markdown (LRM-931)

Still one channel-level primary Goal. Each channel-manager agent may keep a
separate long-form process Markdown under that Goal. This is **not** a second
channel Goal.

Field split (locked):

- `progress_summary` / `current_step` / `blocker` remain the authoritative short
  status (checkpoint / goal patch).
- Process Markdown is readable long-form prose. The two stores never silently
  overwrite each other.

Storage: `channel_goal_process_markdown` unique on `(goal_id, manager_agent_id)`.

Human API (channel-member read; channel owner / workspace owner|admin write):

- `GET /api/channels/{channelId}/goal/process`
- `GET /api/channels/{channelId}/goal/process/{agentId}`
- `PUT /api/channels/{channelId}/goal/process/{agentId}`

Agent API (surface read; manager may write only own roster identity):

- `GET /api/agent/channels/{channelId}/goal/process`
- `GET /api/agent/channels/{channelId}/goal/process/{agentId}`
- `PUT /api/agent/channels/{channelId}/goal/process`
- `PUT /api/agent/channels/{channelId}/goal/process/{agentId}` (must match self)

`expected_version=0` creates; otherwise CAS update. Missing current Goal or
missing document returns explicit 404 (no silent fallback).

CLI: `multica goal process list|get|put --channel ...`.

## 10. Acceptance criteria

1. A group channel with no goal behaves exactly as before.
2. An authorized human or manager agent can create one active channel goal;
   concurrent second creation is rejected.
3. Unauthorized members and ordinary agents cannot revise goal intent.
   A manager agent also cannot rewrite a human-authored title, objective, or
   success criteria; only a human can revise that contract.
4. Every channel task claimed while the goal is active carries the latest goal
   version in its per-turn overlay; paused/terminal goals carry none.
5. An executor can durably checkpoint progress, evidence, blocker, and next
   step without changing objective or criteria.
6. The Channel UI displays and live-refreshes the active/paused goal, including
   criteria progress and blocker state, on web and desktop.
7. Completion requires every success criterion to be listed in
   `completed_criteria` and at least one concrete evidence reference; otherwise
   the API returns `409`.
8. Goal updates use optimistic concurrency and reject stale versions.
9. Backend, frontend schema-fallback, runtime-prompt, and card interaction tests
   cover the contracts above.

## 11. Verification

- Focused backend lifecycle, authority, evidence-gate, CAS, runtime-overlay, and
  CLI tests pass.
- Core schema/query typecheck and tests pass.
- Views typecheck, locale parity, and all 3,112 view tests pass.
- React Doctor reports no finding in the new Goal card. Its one changed-scope
  finding points at the pre-existing realtime effect, which already returns and
  invokes every WebSocket unsubscribe function plus timer cleanup.
- Repository `make check` passes TypeScript typecheck and all TypeScript tests.
  Its Go phase is currently blocked by existing `dev` database-suite failures
  outside this feature (missing legacy FK indexes, shared-fixture collisions,
  and an asynchronous Lark teardown panic); the focused modified Go packages
  pass.
