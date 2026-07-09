# Memory Curation And Evolution Center Integration - Spec

- Date: 2026-07-09
- Status: Draft spec
- Owner: Multica platform
- Related: `docs/superpowers/specs/2026-07-09-agent-memory-curation-plan.md`
- Scope: DB-backed memory evidence collection, API-keyless semantic dedupe, Evolution Center UI review queue, memory/skill usage metrics, and Beijing-time curation scheduling.

## Summary

The first memory curation plan defines the four-stage automatic pipeline:

1. L1 Daily Recorder at 01:00.
2. L2 Review Extractor at 02:00.
3. L3 Promotion Writer at 03:00.
4. L4 Curator at 04:00.

This spec extends that plan in five concrete ways:

- L1 must inspect database history, not just local memory files.
- Semantic dedupe must work without closed-provider API keys.
- The existing Self-evolution / Evolution Center page must become the human review and observability surface for memory and skill evolution.
- Memory and skill evolution units must record usage, success rate, and promotion outcomes.
- Scheduling defaults to Beijing time (`Asia/Shanghai`) for this workspace.

## Requirements

### R1: DB History Evidence Collection Is Required

The pipeline must treat the database as the primary evidence source for historical activity. Local agent memory files are inputs, but they are not sufficient.

L1 Daily Recorder must collect bounded, agent-relevant evidence from these existing sources:

| Source | Tables | Inclusion rule |
|---|---|---|
| Channel and DM messages | `channel`, `channel_member`, `channel_message`, `conversation`, `conversation_member`, `thread_participant` | Messages authored by the agent, directly mentioning the agent, in a DM with the agent, or in threads/channels where the agent participated during the window. |
| Issues and comments | `issue`, `comment`, `issue_to_label`, `issue_dependency` | Issues assigned to the agent, claimed by the agent, commented on by the agent, or explicitly mentioning the agent. |
| Agent tasks | `agent_task_queue` | Tasks for the agent that were queued, started, completed, failed, or cancelled during the window. |
| Runtime usage | `task_usage`, `task_usage_hourly`, `runtime_usage` where applicable | Token/cost/runtime evidence used for performance and cost metrics, not for raw memory content. |
| Activity events | `agent_activity_event`, `activity_log` | Lifecycle and platform decision events scoped to the agent or its tasks. |
| Evolution submissions | `evolution_unit_submission`, `shared_evolution_unit`, `agent_skill_suggestion` | Existing memory/skill candidates and promotion decisions that affect review state. |
| Agent memory mirror | `agent_memory` | Server-side canonical memory mirror and sync keys. |

Evidence collection rules:

- Store evidence references as stable IDs such as `channel_message:<uuid>`, `comment:<uuid>`, `task:<uuid>`, `agent_activity_event:<uuid>`.
- Do not copy full transcripts into `daily/*.md`, `REVIEW.md`, `USER.md`, `MEMORY.md`, or `STATE.md`.
- Store short snippets only when they are necessary to understand the claim, with a strict character budget.
- Preserve enough evidence IDs so a later UI can open the source context.
- Apply privacy scope before summarization: DM evidence stays scoped to the addressed agent and participants unless explicitly promoted through governance.

### R2: Evidence Cursor And Idempotency

Add a database-backed cursor so L1 can resume and backfill without duplicating summaries.

Recommended table:

```sql
CREATE TABLE memory_curation_evidence_cursor (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  source_kind TEXT NOT NULL,
  source_id UUID,
  last_seen_at TIMESTAMPTZ,
  last_seen_seq BIGINT,
  last_hash TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, agent_id, source_kind, source_id)
);
```

Notes:

- `source_kind` examples: `channel`, `dm`, `thread`, `issue`, `task_queue`, `activity_event`, `evolution`.
- `source_id` is nullable for workspace-wide streams such as `agent_activity_event` if the cursor is only `(workspace, agent, source_kind)`.
- The existing `memory_curation_watermark` remains the stage/date watermark; this cursor is the lower-level DB evidence cursor.

### R3: Semantic Dedupe Must Work Without API Keys

The product must not require OpenAI, Claude, Gemini, or any closed-provider API key for basic semantic dedupe.

Implement semantic dedupe in three tiers:

| Tier | Dependency | Use case | Required in MVP |
|---|---|---|---|
| Tier 0 deterministic | No model, no network | Exact hash, normalized text hash, metadata overlap, time/status conflict checks. | Yes |
| Tier 1 local lexical semantic | No model, no network | Token normalization, stop-word removal, TF-IDF/cosine, weighted Jaccard, SimHash/MinHash. | Yes |
| Tier 2 optional local embedding | Admin-provided local model files or local embedding service | Better multilingual and paraphrase matching, especially Chinese/English mixed text. | Optional |

MVP behavior without an API key:

- Use exact content hash first.
- Use normalized text hash second.
- Use lexical semantic similarity third. The existing evolution service already has a no-network `semanticSimilarity` style token/cosine implementation; memory curation can reuse or extract this into a shared package.
- If similarity is above a conservative threshold, merge automatically only when destination, type, scope, and sensitivity match.
- If similarity is medium or cross-scope, mark as `needs_human_review` instead of auto-merging.

Optional local embedding behavior:

- Support a configurable local embedding backend later, for example `MULTICA_EMBEDDING_PROVIDER=local` and `MULTICA_EMBEDDING_MODEL_PATH=/models/bge-small-zh-v1.5`.
- Model download must be an explicit admin action, not an automatic background network call.
- Embedding vectors can be cached in DB by `content_hash`.
- The pipeline must still run when the local model is missing; it falls back to Tier 0/Tier 1.

Example:

- Candidate A: `jianghp3 喜欢打篮球。`
- Candidate B: `用户平时爱打篮球。`
- Tier 0 exact hash does not match.
- Tier 1 token similarity plus same user scope identifies a likely duplicate.
- The curator keeps one canonical `USER.md` entry and records both evidence IDs.

### R4: Self-evolution Page Must Include Memory Review

The existing Evolution Center / Self-evolution page should become the main UI for memory and skill learning operations.

Current page: `packages/views/evolution/components/evolution-center-page.tsx`

Add these UI areas:

| Area | Purpose |
|---|---|
| Curation Timeline | Shows L1/L2/L3/L4 status for today and recent days: pending, running, succeeded, failed, skipped. |
| Memory Review Queue | Lists candidate, needs-review, promoted, rejected, expired, and conflict memory entries. |
| Promotion History | Shows what was promoted into `USER.md`, `MEMORY.md`, `STATE.md`, `agent_memory`, or shared evolution units. |
| Curator Diff Preview | Shows before/after summaries for L4 compaction, expiry, and duplicate merges. |
| Memory Metrics | Shows memory usage count, success rate, failure/conflict rate, last used time, and top agents using memory. |
| Skill Metrics | Shows skill usage count, success rate, failure/conflict rate, accepted suggestions, dismissed suggestions, and top agents using skills. |
| Manual Actions | Trigger curation, approve/reject/expire/merge candidates, force re-run a stage, and dry-run a backfill. |

Recommended tab layout:

| Tab | Content |
|---|---|
| Overview | Agent leaderboard, overall learning output, memory/skill success cards, curator health. |
| Agents | Per-agent curation status, memory count, skill count, success rate, cost per successful task. |
| Learning | Combined memory and skill review queue with filters. |
| Memory | Dedicated memory queue, promotion history, `USER.md` / `MEMORY.md` / `STATE.md` destination badges. |
| Skills | Skill drafts, suggestions, accepted/dismissed state, success metrics. |
| Ops | L1-L4 timeline, manual run controls, backfill/dry-run results, failures. |

### R5: UI Review Actions

Memory review queue actions:

| Action | Effect |
|---|---|
| Promote to USER | Write to the source agent's `USER.md` and mirror to `agent_memory` if configured. |
| Promote to MEMORY | Write to `MEMORY.md` for durable agent/project/team knowledge. |
| Promote to STATE | Write to `STATE.md` with lifecycle metadata. |
| Needs more evidence | Keep in review and request/await more evidence. |
| Reject | Mark rejected, write audit, remove from live `REVIEW.md`. |
| Expire | Mark expired, write audit, remove from live `REVIEW.md`. |
| Merge | Merge into an existing memory entry and append evidence references. |

Skill review queue actions:

| Action | Effect |
|---|---|
| Promote skill | Convert a governed submission into a workspace skill and create agent suggestions. |
| Accept suggestion | Bind the skill to the target agent with `agent_skill.source='evolution'`. |
| Dismiss suggestion | Mark suggestion dismissed. |
| Quarantine | Stop a risky skill from being suggested until reviewed. |

All actions must create visible audit records and update DB status, not only edit local markdown files.

### R6: Memory And Skill Usage Feedback

The self-evolution page needs usage and success metrics, so every injected or used memory/skill should be observable.

Add a generic feedback event table:

```sql
CREATE TABLE evolution_unit_feedback_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
  unit_type TEXT NOT NULL CHECK (unit_type IN ('memory', 'skill', 'workflow', 'tool_pattern', 'preference')),
  unit_id UUID,
  local_unit_id TEXT NOT NULL DEFAULT '',
  event TEXT NOT NULL CHECK (event IN ('injected', 'used', 'ignored', 'success', 'failure', 'conflict')),
  outcome TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('', 'success', 'failure', 'neutral')),
  source TEXT NOT NULL DEFAULT 'runtime' CHECK (source IN ('runtime', 'curator', 'manual', 'system')),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_evolution_feedback_workspace_unit
  ON evolution_unit_feedback_event(workspace_id, unit_type, unit_id, created_at DESC);

CREATE INDEX idx_evolution_feedback_agent
  ON evolution_unit_feedback_event(workspace_id, agent_id, created_at DESC);
```

How to count memory usage:

- `injected`: memory was included in the runtime prompt/context.
- `used`: runtime or agent explicitly referenced the memory in a result or tool decision.
- `success`: task succeeded and the memory was used or injected with positive attribution.
- `failure`: task failed and the memory was used or suspected harmful.
- `conflict`: memory contradicted live instructions, source evidence, or another canonical memory.

How to count skill usage:

- `injected`: skill was available to the agent at task start.
- `used`: skill was selected/read/executed during the run.
- `success`: task succeeded after skill use.
- `failure`: task failed after skill use or skill instructions caused an issue.
- `ignored`: skill was suggested but not used.

Aggregates:

- Continue using `shared_evolution_unit.success_count`, `failure_count`, `ignored_count`, `conflict_count`, and `last_used_at` for shared units.
- Add query endpoints that aggregate feedback events for local `agent_memory` and regular `skill` rows as well.
- Update the Evolution Center scoreboard to blend task success, learning output, cost, and memory/skill effectiveness.

Example metric card:

```text
Memory Used: 128
Memory Success Rate: 84%
Top Memory: "PR reports must include validation results" - used 19 times, 95% success
Skill Used: 73
Top Skill: "multica-working-on-issues" - used 31 times, 87% success
```

### R7: Beijing Time Scheduling

For this workspace, scheduled memory curation should use Beijing time.

Implementation rule:

- Default memory curation timezone: `Asia/Shanghai`.
- L1 runs at `01:00 Asia/Shanghai`.
- L2 runs at `02:00 Asia/Shanghai`.
- L3 runs at `03:00 Asia/Shanghai`.
- L4 runs at `04:00 Asia/Shanghai`.
- Audit rows must store `timezone: "Asia/Shanghai"` and the Beijing-local `plan_date`.

Do not block MVP on a full workspace timezone settings UI. A later product iteration can add per-workspace overrides, but the first implementation should be deterministic and Beijing-time aligned.

## API Additions

### Curation Runs

| Endpoint | Purpose |
|---|---|
| `GET /api/workspaces/{workspaceId}/memory-curation/status` | Workspace-level L1-L4 status, pending candidates, failures. |
| `POST /api/workspaces/{workspaceId}/memory-curation/runs` | Start manual curation, dry-run, backfill, or one stage. |
| `GET /api/workspaces/{workspaceId}/memory-curation/runs/{runId}` | Run details, per-agent counters, error messages. |
| `POST /api/workspaces/{workspaceId}/memory-curation/runs/{runId}/cancel` | Cancel a queued/running manual job. |

### Review Queue

| Endpoint | Purpose |
|---|---|
| `GET /api/evolution/submissions?workspace_id=...&unit_type=memory&status=needs_review` | Existing queue extended for memory review. |
| `POST /api/evolution/submissions/{id}/promote-memory` | Promote to a memory destination with explicit destination. |
| `POST /api/evolution/submissions/{id}/reject` | Existing reject action; ensure it removes/compacts live `REVIEW.md`. |
| `POST /api/evolution/submissions/{id}/expire` | Mark time-sensitive candidate expired. |
| `POST /api/evolution/submissions/{id}/merge` | Merge into an existing memory or evolution unit. |

### Metrics

| Endpoint | Purpose |
|---|---|
| `GET /api/evolution/metrics?workspace_id=...` | Workspace totals for memory/skill usage, success, failure, conflicts. |
| `GET /api/evolution/metrics/agents?workspace_id=...` | Per-agent memory/skill effectiveness. |
| `GET /api/evolution/metrics/units?workspace_id=...&unit_type=memory` | Top/bottom memory units by use and success. |
| `GET /api/evolution/metrics/units?workspace_id=...&unit_type=skill` | Top/bottom skills by use and success. |

## UI Data Model

Add frontend types for:

```ts
type MemoryCurationStage = "l1_daily" | "l2_review" | "l3_promote" | "l4_curator";
type MemoryCurationStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled" | "skipped";

type EvolutionUnitMetric = {
  unit_id: string | null;
  local_unit_id: string;
  unit_type: "memory" | "skill" | "workflow" | "tool_pattern" | "preference";
  title: string;
  injected_count: number;
  used_count: number;
  success_count: number;
  failure_count: number;
  ignored_count: number;
  conflict_count: number;
  success_rate: number;
  last_used_at: string | null;
};
```

## Acceptance Criteria

- L1 reads DB evidence for messages, issues, comments, tasks, activity events, runtime usage, and evolution submissions.
- L1 daily files include evidence references and bounded snippets, not raw transcript dumps.
- Semantic dedupe works on a machine with no external API key and no downloaded embedding model.
- Optional local embedding can be added later without changing the core pipeline contract.
- The Self-evolution page shows memory review, skill review, curation timeline, promotion history, usage counts, and success rates.
- A user/admin can manually approve, reject, expire, or merge memory candidates from the UI.
- Memory and skill feedback events drive usage count and success-rate metrics.
- The automatic schedule runs at 01:00/02:00/03:00/04:00 Beijing time.
- Processed, rejected, expired, and merged review entries are removed from live `REVIEW.md` after audit.
- All writes remain agent-scoped unless explicitly promoted through governance.

## Rollout Plan

1. Add DB evidence collection queries and evidence cursor.
2. Add no-API-key dedupe helpers and thresholds.
3. Add feedback event table and metrics queries.
4. Extend curation run/status APIs.
5. Extend Evolution Center UI with Memory, Skills, and Ops review surfaces.
6. Wire runtime memory/skill injection and task completion to feedback events.
7. Enable Beijing-time scheduled runs.
8. Backfill a small date range in dry-run mode, inspect UI, then enable full automatic promotion gates.

## Open Questions

- Should memory review actions live only in Evolution Center, or should the Agent Memory tab expose a per-agent subset too?
- Which memory entries should count as `used`: explicit citation only, or prompt injection plus successful task attribution?
- Do we want to expose local embedding setup in admin UI, or keep it as server environment configuration for now?
