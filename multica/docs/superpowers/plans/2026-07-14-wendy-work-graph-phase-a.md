# Wendy Work Graph Phase A — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship an event-driven work graph so Wendy unlocks waiting agents with a visible `@` only when all `waits_on` prerequisites are `done` — and stays silent while waits are still valid.

**Architecture:** Cheap domain signals upsert `work_node` / `work_edge` and enqueue `pending_handoff` rows with rule-based detectors (no whole-workspace Radar prompt). A short-interval dispatcher claims due `fast` unlock intents, runs a small Wendy composer prompt over the subgraph only, then reuses the existing transactional `@agent` channel publish + wake path from `#527`.

**Tech Stack:** Go, PostgreSQL/pgx/sqlc, existing Chi handlers, `TaskService` / channel mention wake, scheduler jobs, `go test`.

**Spec:** `docs/superpowers/specs/2026-07-14-wendy-work-graph-supervisor-design.md` (Phase A only).

**Out of scope for this plan (Phase B/C):** `@member`, NLP chat commitments, `blocked`/`needs_rework`/`interrupt_stop`, slow 10-minute nudge lane, retiring whole-workspace Radar, Wendy DM instruction rewrite.

**Base branch:** `origin/dev`. Create branch `feat/wendy-work-graph-phase-a`.

---

## File map

| Path | Responsibility |
|------|----------------|
| `server/migrations/170_wendy_work_graph.up.sql` / `.down.sql` | `work_node`, `work_edge`, `pending_handoff` (+ indexes, checks) |
| `server/pkg/db/queries/work_graph.sql` | sqlc CRUD / claim / list open waits |
| `server/pkg/db/queries/issue_dependency.sql` | list deps for sync (table already in `001_init`) |
| `server/internal/workgraph/types.go` | constants + structs |
| `server/internal/workgraph/store.go` | DB access wrappers used by services |
| `server/internal/workgraph/sync_issue.go` | upsert issue nodes + sync `waits_on` from `issue_dependency` |
| `server/internal/workgraph/detect_unlock.go` | when all waits resolved → enqueue `unlock` |
| `server/internal/workgraph/*_test.go` | pure + DB tests for sync/detect |
| `server/internal/handler/wendy_handoff_dispatch.go` | claim due handoffs, compose, publish `@agent` |
| `server/internal/handler/wendy_handoff_dispatch_test.go` | scenario tests 1–3, 5 (agent-only) |
| `server/internal/handler/wendy_handoff_hooks.go` | call sync/detect from issue status + task terminal paths |
| `server/internal/scheduler/jobs_wendy_handoff.go` | poll due `pending_handoff` every ~15s |
| `server/cmd/server/main.go` | register scheduler job |
| `server/internal/radar/action_plan.go` | unchanged for Phase A (old Radar may still run; do not delete yet) |

**Dependency interpretation (lock this):**

- `issue_dependency` row `(issue_id=C, depends_on_issue_id=A, type='blocked_by')` → edge `C waits_on A`.
- `type='blocks'` row `(issue_id=A, depends_on_issue_id=C)` → edge `C waits_on A` (same semantic: A blocks C).
- `type='related'` → **ignore** for waits.

**Issue status → node status:**

- `done` / `cancelled` → node `done` / `cancelled`
- else if open `waits_on` to non-done prereq → `waiting`
- else → `active`

---

### Task 1: Schema + sqlc

**Files:**
- Create: `server/migrations/170_wendy_work_graph.up.sql`
- Create: `server/migrations/170_wendy_work_graph.down.sql`
- Create: `server/pkg/db/queries/work_graph.sql`
- Create: `server/pkg/db/queries/issue_dependency.sql`
- Create: `server/cmd/migrate/work_graph_migration_test.go`

- [ ] **Step 1: Write failing migration test**

```go
func TestWorkGraphMigrationUpSeedDown(t *testing.T) {
	// Use the same migrate harness as visible_autonomy / wendy radar migration tests.
	// Seed: one workspace, two issue-backed work_nodes, one open waits_on edge,
	// one pending_handoff unlock row. Assert UNIQUE(workspace_id, kind, linked_issue_id)
	// where linked_issue_id IS NOT NULL, and claim query returns the due row.
}
```

- [ ] **Step 2: Run test — expect FAIL (migration missing)**

```bash
cd server && go test ./cmd/migrate -run TestWorkGraphMigrationUpSeedDown -count=1
```

Expected: FAIL (file or table missing).

- [ ] **Step 3: Add up migration**

`170_wendy_work_graph.up.sql` must create:

```sql
CREATE TABLE work_node (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('issue', 'chat_commitment', 'agent_task')),
  title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 500),
  description TEXT NOT NULL DEFAULT '',
  owner_type TEXT NOT NULL CHECK (owner_type IN ('member', 'agent', 'unassigned')),
  owner_id UUID,
  status TEXT NOT NULL CHECK (status IN ('active', 'waiting', 'blocked', 'done', 'needs_rework', 'cancelled')),
  primary_channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  linked_issue_id UUID REFERENCES issue(id) ON DELETE CASCADE,
  linked_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
  last_progress_at TIMESTAMPTZ,
  last_progress_summary TEXT NOT NULL DEFAULT '',
  last_wendy_nudge_at TIMESTAMPTZ,
  last_wendy_nudge_kind TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX work_node_issue_uidx
  ON work_node (workspace_id, linked_issue_id)
  WHERE kind = 'issue' AND linked_issue_id IS NOT NULL;

CREATE TABLE work_edge (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  from_node_id UUID NOT NULL REFERENCES work_node(id) ON DELETE CASCADE,
  to_node_id UUID NOT NULL REFERENCES work_node(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('waits_on', 'blocked_by', 'rework_of')),
  status TEXT NOT NULL CHECK (status IN ('open', 'resolved')),
  evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (from_node_id <> to_node_id)
);

CREATE UNIQUE INDEX work_edge_open_uidx
  ON work_edge (workspace_id, from_node_id, to_node_id, kind)
  WHERE status = 'open';

CREATE TABLE pending_handoff (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  urgency TEXT NOT NULL CHECK (urgency IN ('fast', 'slow')),
  reason_code TEXT NOT NULL CHECK (reason_code IN (
    'unlock', 'block_route', 'interrupt_stop', 'stalled_ask_why', 'progress_nudge'
  )),
  target_actor_type TEXT NOT NULL CHECK (target_actor_type IN ('member', 'agent')),
  target_actor_id UUID NOT NULL,
  related_node_ids UUID[] NOT NULL DEFAULT '{}',
  channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
  dedupe_key TEXT NOT NULL,
  not_before TIMESTAMPTZ NOT NULL DEFAULT now(),
  status TEXT NOT NULL CHECK (status IN ('pending', 'claimed', 'done', 'cancelled')),
  claim_token UUID,
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX pending_handoff_active_dedupe_uidx
  ON pending_handoff (workspace_id, dedupe_key)
  WHERE status IN ('pending', 'claimed');

CREATE INDEX pending_handoff_due_idx
  ON pending_handoff (status, urgency, not_before)
  WHERE status = 'pending';
```

Down migration drops the three tables in FK-safe order (`pending_handoff`, `work_edge`, `work_node`).

- [ ] **Step 4: Add sqlc queries**

`work_graph.sql` (minimum):

- `UpsertIssueWorkNode`
- `GetWorkNodeByIssue`
- `UpsertOpenWaitsOnEdge`
- `ResolveWaitsOnEdge`
- `ListOpenWaitsOnFromNode`
- `CountOpenUnresolvedWaitsOn` (from_node, join to_node.status not in done/cancelled)
- `InsertPendingHandoff` (ON CONFLICT DO NOTHING via unique active dedupe)
- `ClaimDuePendingHandoffs` (FOR UPDATE SKIP LOCKED, status pending → claimed, `not_before <= now()`, limit 10)
- `MarkPendingHandoffDone` / `MarkPendingHandoffCancelled`
- `TouchWorkNodeWendyNudge`

`issue_dependency.sql`:

```sql
-- name: ListIssueDependenciesForWorkspace :many
SELECT id, issue_id, depends_on_issue_id, type
FROM issue_dependency d
JOIN issue i ON i.id = d.issue_id
WHERE i.workspace_id = $1;
```

Also add a focused query by issue id for incremental sync.

- [ ] **Step 5: Regenerate and re-run migration test**

```bash
make sqlc
cd server && go test ./cmd/migrate -run TestWorkGraphMigrationUpSeedDown -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/migrations/170_wendy_work_graph.up.sql server/migrations/170_wendy_work_graph.down.sql \
  server/pkg/db/queries/work_graph.sql server/pkg/db/queries/issue_dependency.sql \
  server/pkg/db/generated/ server/cmd/migrate/work_graph_migration_test.go
git commit -m "$(cat <<'EOF'
feat(workgraph): add Wendy work graph schema

EOF
)"
```

---

### Task 2: Sync issue nodes + dependency edges

**Files:**
- Create: `server/internal/workgraph/types.go`
- Create: `server/internal/workgraph/store.go`
- Create: `server/internal/workgraph/sync_issue.go`
- Create: `server/internal/workgraph/sync_issue_test.go`

- [ ] **Step 1: Write failing tests**

Cover:

1. Upsert issue A/B/C nodes from issue rows (assignee agent → `owner_type=agent`).
2. `blocked_by` dependency C→A and C→B creates two open `waits_on` edges; C status becomes `waiting`.
3. Marking A `done` resolves edges to A; C still `waiting` while B open.
4. Marking B `done` resolves remaining waits; C status becomes `active` (or stays waiting until detector runs — assert edges resolved + `CountOpenUnresolvedWaitsOn==0`).

Use the handler/radar DB test harness pattern (`testPool`, seed workspace/agents/issues).

- [ ] **Step 2: Run tests — expect FAIL**

```bash
cd server && go test ./internal/workgraph -run TestSyncIssue -count=1
```

- [ ] **Step 3: Implement sync**

```go
package workgraph

func (s *Store) SyncIssueNode(ctx context.Context, issue db.Issue) (db.WorkNode, error)
func (s *Store) SyncDependenciesForIssue(ctx context.Context, workspaceID, issueID pgtype.UUID) error
func (s *Store) RecomputeIssueNodeStatus(ctx context.Context, nodeID pgtype.UUID) error
```

Rules:

- Never set `owner_id` to the workspace Wendy/supervisor agent for execution ownership.
- When prerequisite node becomes `done`/`cancelled`, set matching open `waits_on` edges to `resolved`.
- Idempotent: re-sync must not duplicate open edges (unique index).

- [ ] **Step 4: Re-run tests — expect PASS**

```bash
cd server && go test ./internal/workgraph -count=1
```

- [ ] **Step 5: Commit**

```bash
git add server/internal/workgraph
git commit -m "$(cat <<'EOF'
feat(workgraph): sync issue nodes and waits_on edges

EOF
)"
```

---

### Task 3: Unlock detector (rules only)

**Files:**
- Create: `server/internal/workgraph/detect_unlock.go`
- Create: `server/internal/workgraph/detect_unlock_test.go`

- [ ] **Step 1: Write failing scenario tests**

```go
func TestDetectUnlockSilentWhilePrereqsOpen(t *testing.T) { /* a done, b open → 0 pending */ }
func TestDetectUnlockEnqueuesWhenAllPrereqsDone(t *testing.T) { /* a+b done → 1 unlock for c owner */ }
func TestDetectUnlockDedupe(t *testing.T) { /* second detect → still 1 active pending */ }
func TestDetectUnlockSkipsWendyOwner(t *testing.T) { /* waiter owner is supervisor → no enqueue */ }
```

`dedupe_key` format (lock):

```text
unlock:{waiter_node_id}:{sorted_resolved_prereq_node_ids_joined_by_comma}
```

Target actor = waiter node's owner (must be `agent` in Phase A; if `member`/`unassigned`, skip enqueue and leave a metric/log — Phase B handles members).

`channel_id` = waiter `primary_channel_id` if set; else leave null and let dispatch fall back to issue comment path **only if** you also implement issue-comment unlock in Task 4. Prefer requiring `primary_channel_id` for Phase A channel `@` tests (seed channel membership for Wendy + target).

- [ ] **Step 2: Run — expect FAIL**

```bash
cd server && go test ./internal/workgraph -run TestDetectUnlock -count=1
```

- [ ] **Step 3: Implement**

```go
func (s *Store) DetectUnlockForNode(ctx context.Context, waiterNodeID pgtype.UUID) error
```

Pseudo:

1. Load waiter node; if status in `done|cancelled` return.
2. If `CountOpenUnresolvedWaitsOn > 0` return (no enqueue).
3. If there were never any `waits_on` edges, return (do not unlock random active issues).
4. Insert `pending_handoff` urgency=`fast`, reason=`unlock`, `not_before=now()`.

Helper: `HasAnyWaitsOnEdge` (open or resolved) to distinguish “had deps” vs “no deps”.

- [ ] **Step 4: PASS + commit**

```bash
cd server && go test ./internal/workgraph -count=1
git add server/internal/workgraph
git commit -m "$(cat <<'EOF'
feat(workgraph): enqueue fast unlock handoffs

EOF
)"
```

---

### Task 4: Dispatcher — claim, compose, visible `@agent`

**Files:**
- Create: `server/internal/handler/wendy_handoff_dispatch.go`
- Create: `server/internal/handler/wendy_handoff_compose.go`
- Create: `server/internal/handler/wendy_handoff_dispatch_test.go`
- Modify: `server/internal/scheduler/jobs_wendy_handoff.go` (create)
- Modify: `server/cmd/server/main.go` (register job)
- Modify: `server/cmd/server/router.go` only if a debug admin endpoint is needed (prefer none)

- [ ] **Step 1: Write failing integration tests**

Mirror the confirmed product table (agent-only Phase A):

| Name | Setup | Expect |
|------|-------|--------|
| `TestWendyUnlockSilentParallelWait` | a,b active; c waiting on both | 0 channel msgs from Wendy |
| `TestWendyUnlockSilentPartialDone` | a done, b active | 0 msgs |
| `TestWendyUnlockMentionsCWhenReady` | a+b done | exactly 1 group msg mentioning c; inbox/task wake for c |
| `TestWendyUnlockThenDAfterCDone` | after c done + edge d waits_on c | `@d` once |

Reuse `executePreparedRadarAgentMentionInTx` / mention markdown helpers already in `agent_radar_executor.go` — extract shared “post visible agent mention + wake” into a helper if needed (`publishSupervisorAgentMention`) to avoid duplicating lock order.

Composer Phase A may be **template-first** (no LLM) to keep tests deterministic:

```go
content := fmt.Sprintf("%s 前置已完成，请开始处理：%s", mentionMarkdown("agent", targetID, name), issueTitle)
```

Add a seam `WendyComposer` interface:

```go
type WendyComposer interface {
    ComposeUnlock(ctx context.Context, in UnlockComposeInput) (string, error)
}
```

Default production implementation may call the model later; Phase A tests inject the template composer. Spec allows LLM later; **do not block Phase A on model flakiness**.

- [ ] **Step 2: Run — expect FAIL**

```bash
cd server && go test ./internal/handler -run TestWendyUnlock -count=1
```

- [ ] **Step 3: Implement dispatcher**

```go
func (h *Handler) DispatchDueWendyHandoffs(ctx context.Context, limit int32) (int, error)
```

Flow:

1. `ClaimDuePendingHandoffs` where `urgency='fast'` and `reason_code='unlock'`.
2. For each: validate supervisor binding (reuse `validateScheduledRadarSupervisorWithDB` or workspace_radar_state supervisor).
3. Ensure Wendy + target are channel members; else cancel handoff with reason log.
4. Compose content; publish via transactional mention helper; mark handoff `done`; `TouchWorkNodeWendyNudge`.
5. On publish failure: cancel or return to `pending` with backoff (`not_before=now()+1m`) — pick **return to pending with backoff** once to avoid lost unlocks.

Scheduler job (~15s):

```go
const JobNameWendyHandoffDispatch = "wendy_handoff_dispatch"
```

Register beside `AgentRadarScheduleJob` in `main.go`.

- [ ] **Step 4: PASS**

```bash
cd server && go test ./internal/handler -run TestWendyUnlock -count=1
cd server && go test ./internal/scheduler -run TestWendyHandoff -count=1
```

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/wendy_handoff_dispatch.go \
  server/internal/handler/wendy_handoff_compose.go \
  server/internal/handler/wendy_handoff_dispatch_test.go \
  server/internal/scheduler/jobs_wendy_handoff.go \
  server/cmd/server/main.go
git commit -m "$(cat <<'EOF'
feat(workgraph): dispatch fast unlock mentions via Wendy

EOF
)"
```

---

### Task 5: Wire domain hooks (issue + task completion)

**Files:**
- Create: `server/internal/handler/wendy_handoff_hooks.go`
- Modify: issue status update path (find existing handler/service that sets issue status — typically `server/internal/handler/issue.go` or service layer)
- Modify: agent task terminal path (`server/internal/service/task.go` complete/fail or daemon complete handler)
- Test: `server/internal/handler/wendy_handoff_hooks_test.go`

- [ ] **Step 1: Write failing hook test**

Create issues + `issue_dependency`, transition A then B to `done` through the **same production function** hooks will call (not only Store APIs), assert pending unlock appears after B completes and dispatcher sends `@c`.

- [ ] **Step 2: Implement hooks**

After successful issue status commit:

```go
h.workGraph.SyncIssueNode(...)
h.workGraph.SyncDependenciesForIssue(...)
// For each node that waited on this issue:
h.workGraph.DetectUnlockForNode(waiterID)
```

After agent task terminal success linked to an issue: touch `last_progress_at` on issue node, then run the same detect for waiters.

Keep hooks fail-soft: log errors, never fail the user-facing issue update.

- [ ] **Step 3: PASS + commit**

```bash
cd server && go test ./internal/handler -run 'TestWendyUnlock|TestWendyHandoffHook' -count=1
git add server/internal/handler/wendy_handoff_hooks.go \
  server/internal/handler/wendy_handoff_hooks_test.go \
  # plus touched issue/task files
git commit -m "$(cat <<'EOF'
feat(workgraph): sync graph on issue and task completion

EOF
)"
```

---

### Task 6: Phase A verification gate

- [ ] **Step 1: Run targeted suites**

```bash
cd server && go test ./internal/workgraph ./internal/handler ./internal/scheduler ./cmd/migrate \
  -run 'WorkGraph|WendyUnlock|WendyHandoff|DetectUnlock|SyncIssue' -count=1
cd server && go vet ./internal/workgraph ./internal/handler ./internal/scheduler
```

Expected: PASS.

- [ ] **Step 2: Manual checklist (local or staging)**

1. Ensure Wendy bound (`EnsureWindy`) and in the group with agents a/b/c/d.
2. Create issues with `issue_dependency` so c `blocked_by` a and b; d `blocked_by` c.
3. Assign owners; set a/b in progress — confirm **no** Wendy unlock message.
4. Complete a only — still silent.
5. Complete b — Wendy `@c` once.
6. Complete c — Wendy `@d` once.

- [ ] **Step 3: Open PR to `dev`**

Title: `feat(agents): Wendy work-graph unlock handoffs (phase A)`

Body must link the design spec and list scenario coverage 1–3 and 5 (agent `@` only). Explicitly note Phase B/C not included.

---

## Spec coverage (Phase A)

| Spec item | Task |
|-----------|------|
| WorkNode / WorkEdge / PendingHandoff | Task 1 |
| Sync from issues + depends_on | Task 2 |
| Correct wait → no speak | Task 3–4 tests |
| Partial done → no speak | Task 3–4 tests |
| All prereqs done → `@` waiter | Task 3–5 |
| Fast lane / due dispatch | Task 4 |
| No whole-workspace LLM for unlock | Task 4 template composer |
| Wendy never execution owner | Task 3 skip supervisor owner |
| `@member`, rework, slow lane, chat commitments | **Deferred Phase B/C** |

## Follow-up plans (do not implement in this PR)

1. **Phase B:** `block_route`, `interrupt_stop`, `@member`, channel heuristic extractors.
2. **Phase C:** `chat_commitment`, slow debounce, retire whole-workspace Radar prompt path, update `windyInstructions`.
