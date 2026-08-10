# Agent Memory Curation Pipeline - Plan

- Date: 2026-07-09
- Status: Draft plan
- Owner: Multica platform
- Follow-up: `docs/superpowers/specs/2026-07-09-memory-evolution-center-integration-spec.md`
- Scope: Platform-side automatic and manual memory organization for Multica agent workspaces.

## Background

Multica already gives each managed agent an isolated workspace root under:

```text
<workspaces_root>/<workspace_id>/agents/<agent_id>/
```

The current workspace memory spec defines the local layout:

```text
<agent_root>/
  memory/
    MEMORY.md
    USER.md
    STATE.md
    REVIEW.md
    SCRATCHPAD.md
    daily/
    audit/
  notes/
  sync_queue/
```

Runtime setup points managed providers at this isolated root through `MULTICA_AGENT_ROOT`. All memory, skills, and state paths are relative to it. Pi compatibility variables are derived only at the Pi adapter boundary. The daemon also initializes missing root files through `ensureMulticaAgentRoot`.

However, current behavior is still mostly runtime-local and candidate-driven:

- agents can write local memory files during a run;
- Pi can emit governed candidates to `sync_queue/memory-candidates.jsonl`;
- the daemon uploads sync-queue candidates into `evolution_unit_submission`;
- memory-like evolution units can materialize into `agent_memory` for the submitting agent;
- raw `MEMORY.md`, `USER.md`, `STATE.md`, `REVIEW.md`, and `daily/` files are not automatically summarized, promoted, compacted, or expired by the platform.

This plan adds a platform-owned Memory Curation Pipeline so memory becomes automatic by default, auditable, and recoverable across agents and runtimes.

## Goals

- Automatically record each agent's daily activity and relevant memory evidence starting at 01:00 every day.
- Run the daily memory organization in three ordered stages before the final curator pass.
- Run the final curator once per day to compact long-lived memory files and retire stale state.
- Provide a manual command/API that processes any unorganized backlog for selected agents or the whole workspace.
- Write outputs back to the correct agent's isolated root and, where needed, to `agent_memory` / `evolution_unit_submission`.
- Avoid raw transcript dumps in memory files; use bounded, evidence-linked summaries.
- Keep live agent instructions authoritative over generated memory.

## Non-Goals

- Do not make provider-global memory (`~/.pi`, `~/.claude`, `~/.codex`, etc.) the source of truth.
- Do not inject all historical daily logs into runtime prompts.
- Do not auto-share private agent memory across agents without scope/governance rules.
- Do not require every runtime provider to implement its own cron.
- Do not replace the existing evolution skill governance flow.

## Existing Code Facts

| Area | Current fact | Implication |
|---|---|---|
| Agent root | `multicaAgentRoot` resolves `<workspace_id>/agents/<agent_id>` and `ensureMulticaAgentRoot` seeds memory files. | New jobs should reuse this root and never write provider-global memory. |
| Runtime env | Managed tasks receive only `MULTICA_AGENT_ROOT` as the generic location; Pi compatibility paths are derived at its adapter boundary. | Curated output is visible to subsequent runs through the stable Agent workspace. |
| Scheduler | `server/internal/scheduler` provides a DB-backed `sys_cron_executions` scheduler with distributed leases and audit rows. | Memory jobs should register as scheduler jobs, not ad-hoc goroutines. |
| Server memory table | `agent_memory` stores agent-scoped memory documents with `name`, `sync_key`, `content`, and `config`. | Curated canonical memory can be mirrored/upserted here for UI and server-side retrieval. |
| Evolution submissions | `sync_queue/memory-candidates.jsonl` uploads to `evolution_unit_submission`; memory-like units can promote to `agent_memory`. | Review-stage candidates can use this path for governed promotion. |
| Activity events | `agent_activity_event` stores platform lifecycle events. | Each automatic/manual curation stage should emit events for observability. |

## Memory File Roles

| File | Role | Writer |
|---|---|---|
| `memory/daily/YYYY-MM-DD.md` | Daily activity journal and evidence summary. | L1 Daily Recorder. |
| `memory/REVIEW.md` | Candidate facts/preferences/statuses requiring promotion or conflict handling. | L2 Review Extractor and manual review tools. |
| `users/<member-id>/USER.md` | Durable user preferences and profile facts, isolated by stable workspace member ID. | Scoped self-review and L4 Curator. |
| `memory/MEMORY.md` | Durable agent/project/team decisions, stable operating knowledge, and role-specific facts. | L3 Promotion and L4 Curator. |
| `memory/STATE.md` | Current dated state, temporary facts, quotas, future tasks, active initiatives. | L3 Promotion and L4 Curator. |
| `memory/audit/*.jsonl` | Append-only run metadata, evidence IDs, hashes, and stage results. | All stages. |

## Automatic Pipeline

The platform runs four daily stages. The first three are the memory-recording/organization stages and begin at 01:00. The final curator runs once per day after the stage outputs are stable.

| Stage | Default time | Input | Output | Purpose |
|---|---:|---|---|---|
| L1 Daily Recorder | 01:00 | Previous day chat, DM/thread messages, issue comments/status changes, PR/task summaries, runtime session summaries, existing local notes | `memory/daily/YYYY-MM-DD.md` | Record what this agent did and what memory evidence appeared yesterday. |
| L2 Review Extractor | 02:00 | New or changed `daily/*.md`, `notes/work-log.md`, `SCRATCHPAD.md`, runtime memory candidates | `memory/REVIEW.md`, optional `sync_queue/memory-candidates.jsonl` | Extract candidate memories without immediately polluting durable memory. |
| L3 Promotion Writer | 03:00 | `REVIEW.md`, sync-queue candidates, existing `USER.md` / `MEMORY.md` / `STATE.md`, server `agent_memory` | Updated `USER.md`, `MEMORY.md`, `STATE.md`, `agent_memory` mirror | Promote high-confidence, scoped, non-conflicting entries. |
| L4 Curator | 04:00 | Long-lived memory files and server mirror | Compacted memory files, archived/expired state, audit report | Deduplicate, merge, trim, expire, and keep prompt-sized memory healthy. |

### Timezone

- Default scheduling for this workspace uses Beijing time: `Asia/Shanghai`.
- L1/L2/L3/L4 default times are 01:00/02:00/03:00/04:00 in `Asia/Shanghai`.
- Each job stores `plan_date` and `timezone` in audit metadata.
- For daily windows, `YYYY-MM-DD` means the Beijing-local calendar day unless a future workspace timezone override is explicitly configured.

### Stage L1: Daily Recorder

For every active agent, L1 creates or updates `memory/daily/<date>.md` for the previous workspace-local day. Inactive agents are skipped by default; they only re-enter the pipeline when they produce fresh activity, are explicitly backfilled, or are forced through a manual run.

Inputs should be bounded by the agent's actual relevance:

- messages where the agent authored, was directly mentioned, was assigned, or was the active task recipient;
- thread roots and recent replies for threads the agent participated in;
- issues assigned to the agent, claimed by the agent, commented on by the agent, or explicitly mentioning the agent;
- task lifecycle summaries from `agent_task_queue` and runtime result metadata;
- activity events in `agent_activity_event`;
- existing local `notes/work-log.md` entries and session summaries under the agent root.

The daily file should contain structured sections:

```md
# Daily Memory - 2026-07-08

## Activity Summary
- ...

## Decisions And Stable Facts
- ...

## User / Teammate Preferences Observed
- ...

## Temporary State And Follow-ups
- ...

## Evidence Index
- channel_message:<id> - ...
- issue:<id> - ...
- task:<id> - ...

## Curation Status
- l1_recorded_at: ...
- l2_extracted_at:
- l3_promoted_at:
- l4_curated_at:
```

Rules:

- Summarize; do not paste whole transcripts.
- Include evidence IDs for every memory-worthy claim.
- Preserve human-authored explicit memory requests with high priority.
- If no relevant activity exists, write a small audit row but do not create a noisy daily file unless configured.

### Stage L2: Review Extractor

L2 reads unprocessed daily files and extracts candidate memory entries into `memory/REVIEW.md`.

Candidate types:

| Type | Destination if promoted | Examples |
|---|---|---|
| `preference` | `USER.md` | User likes a communication style, language, sport, review format. |
| `stable_fact` | `MEMORY.md` | Project architecture, repo commands, agent responsibility, team convention. |
| `decision` | `MEMORY.md` or `notes/decisions.md` | Chosen design direction, accepted tradeoff. |
| `temporary` | `STATE.md` | Active task, future date commitment, short-lived blocker. |
| `quota` | `STATE.md` | Monthly API quota or budget with reset date. |
| `conflict` | `REVIEW.md` only | Candidate contradicts live instructions or existing memory. |
| `skill_candidate` | `sync_queue/skill-candidates.jsonl` | Repeatable workflow that should become a skill. |

Recommended `REVIEW.md` entry format:

```md
---
id: mem_20260709_<short_hash>
type: preference
status: candidate
confidence: high
sensitivity: none
scope: agent
source_date: 2026-07-08
evidence:
  - channel_message:<uuid>
proposed_destination: USER.md
---
# User likes basketball

jianghp3 likes playing basketball.
```

Rules:

- Avoid duplicates by hashing normalized `type + destination + content + evidence`.
- Mark sensitive/private candidates and do not promote secrets automatically.
- Keep uncertain or contradictory claims as `status: needs_human_review`.
- For candidates useful beyond one agent, write governed JSONL to `sync_queue/memory-candidates.jsonl` instead of direct cross-agent writes.
- Treat `REVIEW.md` as a short-lived queue, not as a permanent archive.

Review queue lifecycle:

| Status | Meaning | Cleanup behavior |
|---|---|---|
| `candidate` | Newly extracted and awaiting L3 promotion. | Keep until processed or until `review_expires_at`. |
| `promoted` | Written to `USER.md`, `MEMORY.md`, `STATE.md`, or another approved destination. | Remove from `REVIEW.md` after the audit row is written. |
| `rejected` | Not useful, too weak, duplicated, or out of scope. | Remove from `REVIEW.md` after the audit row is written. |
| `expired` | Candidate was time-sensitive and no longer relevant before promotion. | Remove from `REVIEW.md`; never promote. |
| `needs_human_review` | Requires a user or admin decision. | Keep only until the configured review TTL, then archive to audit/escalation. |
| `conflict` | Contradicts live instructions or canonical memory. | Keep until resolved, then remove after audit. |

Cleanup guarantees:

- L3 must not leave processed review entries in `REVIEW.md`; it writes a compact audit record instead.
- L4 must sweep expired or stale review entries that L3 could not process.
- `REVIEW.md` should stay small enough for manual inspection and should not be injected into agent prompts by default.

### Stage L3: Promotion Writer

L3 promotes eligible review entries into canonical memory files and mirrors canonical documents to `agent_memory` when useful for UI/server access.

Promotion gates:

- `confidence=high` or explicit human memory request;
- destination is clear (`USER.md`, `MEMORY.md`, or `STATE.md`);
- not marked secret;
- no unresolved conflict with live agent instructions;
- not a duplicate of an active memory entry;
- scope matches the destination agent.

Destination rules:

- `USER.md`: durable user facts/preferences, written with user identity and source evidence.
- `MEMORY.md`: durable project, team, tool, repo, or agent-operating knowledge.
- `STATE.md`: dated state with lifecycle metadata such as `status`, `date`, `ttl`, `expires_at`, or `reset_at`.
- `notes/decisions.md`: decision records that are too verbose for prompt memory.
- `sync_queue/memory-candidates.jsonl`: workspace/share candidates that require governance.

Promotion should update review entry status to `promoted` and record:

- destination path;
- promoted content hash;
- promoted time;
- server `agent_memory` id when mirrored;
- reason when skipped/rejected.

After recording the audit row, L3 should delete or compact processed `REVIEW.md` entries instead of keeping a growing review history in the live review file.

Canonical memory write policy:

- `MEMORY.md` receives only durable, high-signal agent/project/team knowledge; it must not receive raw conversation summaries, low-confidence candidates, expired review items, or every promoted daily detail.
- `USER.md` receives only stable user preferences/profile facts and should merge updates into existing entries instead of appending duplicates.
- `STATE.md` receives temporary/event/quota entries with explicit lifecycle metadata so they can expire automatically.
- `daily/*.md` is the append-only chronological record; canonical files are curated indexes, not logs.
- Processed, rejected, expired, or superseded material belongs in `memory/audit/*.jsonl` or old `daily/` files, not in active prompt-loaded memory.

### Stage L4: Curator

The curator runs once per day after L3, default 04:00. It keeps canonical memory concise and current.

Responsibilities:

- merge exact or semantic duplicates;
- collapse long repeated entries into one canonical entry with evidence references;
- archive expired `STATE.md` entries;
- mark past events as `archived` or move them to daily/audit;
- remove stale temporary facts from prompt-loaded sections;
- sweep `REVIEW.md` entries that are promoted, rejected, expired, superseded, or past TTL;
- keep `REVIEW.md`, `USER.md`, and `MEMORY.md` within configured size budgets;
- preserve managed markers and human-authored protected sections;
- write an audit report under `memory/audit/curator-YYYY-MM-DD.jsonl`.

Expiration rules:

| Entry kind | Expiration behavior |
|---|---|
| `temporary` with `expires_at` before today | Mark archived; exclude from default memory injection. |
| `event` with date in the past | Move from active state to archived history unless still referenced. |
| `quota` after reset date | Mark reset/past; optionally keep latest active quota only. |
| future task/date commitment | Keep active until date passes, then archive on the next curator run. |
| user preference | Never expire automatically; only update/merge. |
| promoted/rejected review item | Remove from live `REVIEW.md` after audit; keep only compact audit metadata. |
| expired review item | Remove from live `REVIEW.md`; do not promote. |

Default file budgets:

| File | Default budget | Overflow behavior |
|---|---:|---|
| `REVIEW.md` | 200 open entries or 256 KiB | Process, expire, or move closed items to audit. |
| `USER.md` | 128 KiB | Merge equivalent preferences and keep the latest evidence pointer. |
| `MEMORY.md` | 256 KiB | Summarize related durable facts and move detail to notes/audit. |
| `STATE.md` | 256 KiB | Archive inactive/expired state before prompt injection. |

## Manual Pipeline

A manual trigger must process all unorganized memory for a scope and write results to the corresponding agent roots.

### CLI

Add a `multica memory curate` command group:

```bash
multica memory curate \
  --workspace <workspace-id-or-slug> \
  --all-agents \
  --since 2026-07-01 \
  --stage all \
  --output json
```

Suggested options:

| Option | Meaning |
|---|---|
| `--agent <id-or-slug>` | Curate one agent. Repeatable. |
| `--all-agents` | Curate every agent in the workspace. |
| `--since <date>` | Backfill from this date. Defaults to last unprocessed watermark. |
| `--until <date>` | Stop at this date. Defaults to yesterday for daily stages or now for backlog. |
| `--stage l1|l2|l3|l4|all` | Run a single stage or the full pipeline. |
| `--include-history` | Scan older chat/issue/task history, not only daily files. |
| `--dry-run` | Produce a report without writing memory files. |
| `--force` | Reprocess already processed inputs. |
| `--output json` | Machine-readable run summary. |

Examples:

```bash
# Organize everything not yet processed for one agent.
multica memory curate --agent 1864763b-e698-464b-b49c-27667319cd23 --stage all

# Backfill all agents from July 1, including historical chat/issue context.
multica memory curate --all-agents --since 2026-07-01 --include-history

# Preview only the promotion step.
multica memory curate --all-agents --stage l3 --dry-run --output json
```

### API

Add daemon/server endpoints for UI and automation:

| Endpoint | Purpose |
|---|---|
| `POST /api/workspaces/{workspaceId}/memory-curation/runs` | Start manual curation run. |
| `GET /api/workspaces/{workspaceId}/memory-curation/runs/{runId}` | Inspect status and counters. |
| `POST /api/workspaces/{workspaceId}/memory-curation/runs/{runId}/cancel` | Cancel queued/in-progress manual run. |
| `GET /api/agents/{agentId}/memory-curation/status` | Show watermarks, last success/failure, pending candidates. |

Manual runs should use the same stage code as automatic runs. They differ only in scope, date range, and whether `force`/`dry_run` is enabled.

## Data Model Additions

Add a durable state table for watermarks and manual run tracking.

```sql
CREATE TABLE memory_curation_run (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID REFERENCES agent(id) ON DELETE CASCADE,
  stage TEXT NOT NULL CHECK (stage IN ('l1_daily', 'l2_review', 'l3_promote', 'l4_curator', 'all')),
  trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('scheduled', 'manual', 'backfill')),
  status TEXT NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
  date_from DATE,
  date_to DATE,
  dry_run BOOLEAN NOT NULL DEFAULT false,
  force BOOLEAN NOT NULL DEFAULT false,
  stats JSONB NOT NULL DEFAULT '{}'::jsonb,
  error TEXT NOT NULL DEFAULT '',
  requested_by UUID REFERENCES member(id) ON DELETE SET NULL,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE memory_curation_watermark (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  stage TEXT NOT NULL CHECK (stage IN ('l1_daily', 'l2_review', 'l3_promote', 'l4_curator')),
  last_processed_date DATE,
  last_input_hash TEXT NOT NULL DEFAULT '',
  last_run_id UUID REFERENCES memory_curation_run(id) ON DELETE SET NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, agent_id, stage)
);
```

Scheduler audit remains in `sys_cron_executions`; business-level curation status lives in `memory_curation_run`.

## Scheduler Design

Use `server/internal/scheduler` with four stable job names:

| Job name | Cadence | Catch-up | Scope | Default schedule behavior |
|---|---:|---|---|---|
| `memory_l1_daily_record` | 24h | every plan | workspace | Eligible at 01:00 workspace local time. |
| `memory_l2_review_extract` | 24h | every plan | workspace | Eligible at 02:00 workspace local time. |
| `memory_l3_promote` | 24h | every plan | workspace | Eligible at 03:00 workspace local time. |
| `memory_l4_curator` | 24h | every plan | workspace | Eligible at 04:00 workspace local time; once per day. |

Implementation note: the existing scheduler floors UTC cadences. To support workspace-local 01:00 / 02:00 / 03:00 / 04:00 exactly, add either:

1. a `DailyAt` schedule helper that computes due plan dates per workspace timezone, or
2. a thin memory scheduler handler that ticks frequently but claims per-workspace/date rows in `memory_curation_run`.

Prefer option 1 if extending `scheduler.JobSpec` remains small; prefer option 2 if we want to avoid changing generic scheduler semantics.

Concurrency:

- scope by workspace first, then process agents in bounded batches;
- use per `(workspace_id, agent_id, stage, date)` idempotency keys;
- allow multiple workspaces in parallel;
- avoid two stages for the same agent/date running concurrently unless the earlier stage has succeeded;
- manual `force` runs should acquire the same per-agent/date lock and either wait or fail fast.

## Curation Engine

Add a provider-neutral server package, for example:

```text
server/internal/memorycuration/
  daily.go
  review.go
  promote.go
  curator.go
  evidence.go
  files.go
  llm.go
  service.go
```

Responsibilities:

- collect evidence from DB and agent-root files;
- summarize bounded evidence into daily files;
- parse and update managed markdown sections safely;
- generate candidate entries and promotion decisions;
- mirror canonical memory documents into `agent_memory` with stable sync keys such as `users/<member-id>/USER.md`;
- write JSONL audit rows;
- emit `agent_activity_event` lifecycle events.

The package should be deterministic where possible. LLM calls are allowed for summarization and semantic dedupe, but hard gates must remain deterministic for sensitivity, destination, expiry, and authority conflicts.

## Prompt And Injection Policy

After this pipeline exists, runtime prompts should continue to inject only bounded memory:

- concise snapshot from `MEMORY.md`;
- relevant `USER.md` preferences;
- active, non-expired entries from `STATE.md`;
- path index for full memory files;
- never full daily logs by default;
- never full `REVIEW.md` by default.

The curator should maintain a prompt-safe section in each canonical file, for example:

```md
<!-- multica-managed:prompt-summary:start -->
...
<!-- multica-managed:prompt-summary:end -->
```

Runtime agents may append candidates, but should not overwrite managed sections.

## Observability

Each stage writes:

- `sys_cron_executions` row via scheduler;
- `memory_curation_run` row with counters;
- `agent_activity_event` for success/failure at agent scope;
- `memory/audit/<stage>-YYYY-MM-DD.jsonl` with file-level writes, hashes, and evidence IDs.

Minimum counters:

| Counter | Meaning |
|---|---|
| `agents_scanned` | Agents included in scope. |
| `agents_changed` | Agents with file/server writes. |
| `daily_files_written` | L1 output count. |
| `review_candidates_added` | L2 candidate count. |
| `entries_promoted` | L3 promotion count. |
| `entries_archived` | L4 archive/expiry count. |
| `duplicates_merged` | L4 dedupe count. |
| `conflicts_found` | Entries requiring human review. |
| `errors` | Per-agent/stage failures. |

## Safety And Privacy

- Never write one agent's private memory into another agent's root without explicit scope/governance.
- Treat DM evidence as private to participants and the addressed agent unless policy says otherwise.
- Do not promote secrets, tokens, credentials, or raw local paths into durable memory.
- Keep evidence references instead of large verbatim message copies.
- Respect live agent instructions as higher authority; conflicting memory stays in `REVIEW.md`.
- Manual runs require workspace admin/owner or an explicit permission.
- Dry-run mode must not mutate files, DB rows, or sync queues.

## Backfill Strategy

Manual `--include-history` supports historical memory organization for old agents.

Recommended rollout:

1. Backfill only L1 daily summaries for a limited date range.
2. Run L2 in dry-run and inspect candidate volume/conflicts.
3. Enable L3 promotion only for explicit human memory requests and high-confidence preferences.
4. Enable full L3/L4 after audit output is trusted.
5. Keep watermarks so interrupted backfills resume without reprocessing.

## Rollout Plan

### Phase 0: Documentation And Product Approval

- Land this plan and align on schedules: L1 01:00, L2 02:00, L3 03:00, L4 04:00.
- Confirm workspace timezone source and manual command shape.

### Phase 1: Infrastructure

- Add `memory_curation_run` and `memory_curation_watermark` migrations.
- Add `server/internal/memorycuration` package skeleton.
- Add file-safe helpers for managed markdown blocks and JSONL audit writes.
- Register scheduler jobs in `server/cmd/server/main.go`.

### Phase 2: L1 Daily Recorder

- Implement evidence collection for messages, issues, task queue, and activity events.
- Write bounded daily summaries with evidence indexes.
- Add tests for no-activity days, thread scoping, issue scoping, and idempotent reruns.

### Phase 3: L2 Review Extractor

- Parse unprocessed daily files.
- Generate candidate entries in `REVIEW.md` with stable IDs and hashes.
- Support sync-queue output for governed memory candidates.
- Add duplicate/conflict tests.

### Phase 4: L3 Promotion Writer

- Implement deterministic promotion gates.
- Update `USER.md`, `MEMORY.md`, `STATE.md`, `notes/decisions.md` where appropriate.
- Mirror canonical docs or promoted entries to `agent_memory` with stable sync keys.
- Add tests for explicit remember requests, preference promotion, temporary state, and conflict holdback.

### Phase 5: L4 Curator

- Implement dedupe, compaction, expiry, and prompt-summary maintenance.
- Add size budgets and archived-state behavior.
- Add tests for expired dated work, quota reset, and protected human sections.

### Phase 6: Manual Trigger And UI Hooks

- Add `multica memory curate` CLI.
- Add API endpoints for manual runs and status.
- Add an Agent Memory status view showing last run, watermarks, pending candidates, and errors.

## Acceptance Criteria

- At 01:00 workspace-local time, every active agent with relevant prior-day activity gets a daily memory summary or a no-op audit row.
- At 02:00, unprocessed daily summaries produce deduplicated review candidates.
- At 03:00, eligible high-confidence candidates are promoted to the correct memory files for the same agent.
- At 04:00, the curator runs once, compacts memory, and expires stale `STATE.md` entries.
- Manual `multica memory curate --agent <id> --stage all` processes any unorganized backlog and writes to that agent's memory root.
- Manual `--all-agents --include-history` can backfill old agents without duplicating already processed entries.
- Every write is auditable through `memory/audit`, `memory_curation_run`, and activity events.
- Raw transcripts are not copied wholesale into memory files.
- Live agent instructions remain authoritative over curated memory.
- Provider-global memory directories are never touched.

## Open Questions

- Should L3 mirror whole files into `agent_memory`, individual promoted entries, or both?
- Should inactive/offline agents be curated every day or only when they have new evidence?
- Which review actions should also appear in the per-agent Memory tab besides the Evolution Center?
- Which local embedding option, if any, should be packaged first after the no-API-key lexical dedupe MVP?
