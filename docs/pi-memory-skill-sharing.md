# Pi Memory / Skill Sharing Flow

This note documents the Pi-specific memory and skill sharing paths in Multica, excluding the workspace-level `~/.pi/share/skills` scanner.

Last verified against the current working tree on 2026-06-30.

## Scope

This document covers the Pi agent evolution/governance flow:

```text
Local Pi agent root
  sync_queue/memory-candidates.jsonl          ──► memory path (auto-assign)
  sync_queue/skill-candidates.jsonl           ──► skill path (review + suggestions)
  skills/drafts/<skill_id>/
        |
        v
Multica server database
  evolution_unit_submission
  evolution_unit_submission_file
        |
        +── memory / preference / tool_pattern / workflow
        |       hard gates only → promoted → agent_memory (source agent)
        |
        └── skill
                optional LLM advisory review
                → promoted → skill + skill_file
                → agent_skill_suggestion → user confirm → agent_skill
```

It does not cover the global shared-skill scanner under `~/.pi/share/skills`, which syncs directly into workspace `skill` / `skill_file` via the runtime shared-skill sync endpoint.

## Design principles

- **Skill promotion ≠ assignment.** A promoted skill is written to the workspace public `skill` table. Binding it to an agent is a separate, user-confirmed step.
- **Memory auto-assigns to the submitting agent.** After deterministic hard gates pass, memory-like units are promoted and written to `agent_memory` for `source_agent_id` with no LLM or manual review.
- **No per-agent delivery table.** `evolution_unit_delivery` was removed (migration 137). Evolution does not push generated bundles back to `skills/generated/` or `skills/enabled/`. (A separate Prepare/Reuse step may mirror already-bound `agent_skill` rows into `skills/enabled/` for inspection only.)

## Local Pi Agent Root

For each workspace agent, Multica creates a stable Pi agent root:

```text
<WorkspacesRoot>/<workspace_id>/.pi/agents/<agent_id>/
```

The daemon helper is `piAgentRoot(cfg, workspaceID, agentID)` in `server/internal/daemon/shared_skills.go`.

The root contains these directories:

```text
memory/
  daily/
  audit/
skills/
  drafts/
  generated/          # legacy empty scaffold; evolution no longer writes here
  enabled/            # legacy empty scaffold; evolution no longer writes here
inbox/
  memory/
  skills/
shared-cache/
  memory/
  skills/
profile/
feedback/
sync_queue/
```

`ensurePiAgentRoot` still creates `skills/generated/` and `skills/enabled/` for layout compatibility. Evolution does **not** populate them. Task-time skills still come from the database via `agent_skill` and hydrate into provider-native workdir paths (`.pi/skills/`, `.cursor/skills/`, …).

Additionally, on Prepare/Reuse the daemon **mirrors** currently bound skills into `skills/enabled/<slug>/` (marked with `.multica-bound-mirror`) so the agent root shows what is bound. This mirror is one-way (DB → disk), best-effort, and does not change task hydration or write disk edits back to the DB.

The daemon exposes paths to the Pi runtime through environment variables:

```bash
PI_AGENT_ROOT=<agent_root>
PI_MEMORY_DIR=<agent_root>/memory
PI_SKILL_DRAFTS_DIR=<agent_root>/skills/drafts
PI_AGENT_INBOX_DIR=<agent_root>/inbox
PI_AGENT_SHARED_CACHE_DIR=<agent_root>/shared-cache
PI_AGENT_SYNC_QUEUE_DIR=<agent_root>/sync_queue
```

## Upload Sources

Only the sync queue is uploaded to the server in this flow.

### Memory Candidates

Memory candidates are read from:

```text
<agent_root>/sync_queue/memory-candidates.jsonl
```

Each line is a JSON object. If `unit_type` is missing, the daemon defaults it to `memory`.

Example:

```json
{
  "unit_type": "memory",
  "local_unit_id": "mem_20260625_001",
  "title": "Prefer non-destructive git operations",
  "summary": "Avoid resetting user changes unless explicitly requested.",
  "content": "When working in a dirty tree, do not revert changes that the agent did not create.",
  "sensitivity": "none",
  "confidence": "high",
  "suggested_scope": "workspace",
  "tags": ["git", "safety"],
  "task_types": ["code-editing"],
  "created_at": "2026-06-25T00:00:00Z"
}
```

### Skill Candidates

Skill candidates are read from:

```text
<agent_root>/sync_queue/skill-candidates.jsonl
```

Each line is a manifest. If `unit_type` is missing, the daemon defaults it to `skill`.

The skill body is not expected to live directly inside the JSONL file. Instead, the manifest may point to a bundle directory with `bundle_path`.

Recommended layout:

```text
<agent_root>/skills/drafts/<skill_id>/
  SKILL.md
  references/...
  scripts/...

<agent_root>/sync_queue/skill-candidates.jsonl
```

Example manifest:

```json
{
  "unit_type": "skill",
  "local_unit_id": "skill_lsp_navigation_001",
  "title": "LSP navigation workflow",
  "summary": "Use diagnostics, definitions, references, and rename for safer refactors.",
  "bundle_path": "../skills/drafts/skill_lsp_navigation_001",
  "sensitivity": "none",
  "confidence": "high",
  "tags": ["lsp", "refactor"],
  "tools": ["lsp"],
  "task_types": ["code-navigation", "refactor"],
  "created_at": "2026-06-25T00:00:00Z"
}
```

When `bundle_path` is present, the daemon resolves it relative to `sync_queue/`, reads the draft directory, includes `SKILL.md`, collects supporting files, and sends them as submission files.

Skill candidates generated by the L3 memory curator carry `evidence.requires_human_review=true`. The Evolution service treats this as a deterministic hard gate: the submission enters `needs_review` before any optional LLM reviewer runs and cannot be promoted until an administrator uses the existing manual review action.

The daemon runs local memory curation for workspaces that have an online Pi runtime. L1/L2/L3/L4 become due at 01:00/02:00/03:00/04:00 Asia/Shanghai and catch up later that local day after daemon downtime. L3 uses that daemon account's configured Pi executable, authentication, and default model. Daemon review defaults on and can be disabled with `MULTICA_DAEMON_MEMORY_CURATION_L3_REVIEW_ENABLED=false`; server/manual review remains opt-in through `MEMORY_CURATION_L3_REVIEW_ENABLED=true` because it may run under another OS account. Deterministically extracted L2 entries default to `sensitivity: unknown`, so only candidates explicitly classified `sensitivity: none` are sent to the model. Review failures are fail-closed and leave candidates in `REVIEW.md` for retry. A per-agent local lock prevents server, CLI, and daemon curator processes that share a workspaces root from mutating the same root concurrently.

## Paths That Are Not Uploaded Directly

These paths are for Pi runtime state or legacy layout. They are not direct upload sources and are not written by the evolution downflow in the current implementation:

- `<agent_root>/memory/MEMORY.md`
- `<agent_root>/memory/USER.md`
- `<agent_root>/memory/STATE.md`
- `<agent_root>/memory/REVIEW.md`
- `<agent_root>/memory/daily/`
- `<agent_root>/skills/generated/`
- `<agent_root>/skills/enabled/`
- `<agent_root>/inbox/`
- `<agent_root>/shared-cache/`
- `<agent_root>/profile/`
- `<agent_root>/feedback/`

If local Pi memory should be shared, Pi must emit a governed candidate into `sync_queue/memory-candidates.jsonl`. Multica does not mirror raw memory files as a folder sync.

## Upload API

The daemon uploads candidate submissions through:

```http
POST /api/daemon/runtimes/{runtimeId}/evolution/submissions
```

The server handler is `SyncEvolutionSubmissions` in `server/internal/handler/evolution.go`.

The daemon only runs this flow for Pi runtimes with a workspace id. It scans agent roots under:

```text
<WorkspacesRoot>/<workspace_id>/.pi/agents/
```

There is no daemon polling endpoint for evolution deliveries; that path was removed with `evolution_unit_delivery`.

## Database Writes: Inbound Submissions

### `evolution_unit_submission`

Each memory or skill candidate is upserted into `evolution_unit_submission`.

Important fields:

- `workspace_id` — tenant scope.
- `source_agent_id` — agent that produced the candidate.
- `source_member_id` — runtime owner member, when resolvable.
- `unit_type` — `memory`, `skill`, `workflow`, `tool_pattern`, or `preference`.
- `local_unit_id` — local candidate id; unique with workspace and source agent.
- `title`, `summary`, `content` — human-readable candidate body.
- `payload`, `sanitized_payload` — raw/sanitized source payload.
- `content_hash` — dedupe hash for memory/preference/tool-pattern style units.
- `bundle_hash` — dedupe hash for skill/workflow bundles.
- `bundle_ref` — source bundle path/reference.
- `sensitivity`, `confidence`, `suggested_scope` — governance inputs.
- `evidence`, `applies`, `tags`, `tools`, `task_types`, `project_types`, `languages`, `frameworks` — matching and audit metadata.
- `status` — starts as `candidate`, may become `needs_review`, `rejected`, or `promoted`.
- `review_decision`, `review_confidence`, `review_risk_level`, `review_reason`, `review_metadata`, `reviewed_at` — structured governance review metadata.
- `promoted_unit_id` — points to `shared_evolution_unit` after promotion.

### `evolution_unit_submission_file`

Skill bundle files are written into `evolution_unit_submission_file`.

The upload handler deletes existing files for the submission and rewrites the incoming file set on each upsert.

Important fields:

- `submission_id`
- `path`
- `content`
- `content_hash`
- `mime_type`
- `size_bytes`

For skill submissions, a valid bundle must include `SKILL.md`; otherwise the curator rejects it.

## Immediate Server-Side Processing

After a non-secret submission is committed, the handler calls:

```go
service.NewEvolutionService(h.Queries).CurateAndMatchWorkspace(ctx, rt.WorkspaceID, 50)
```

This performs a deterministic governance pass, optionally followed by an LLM advisory reviewer when `EVOLUTION_REVIEW_ENABLED=true`.

### Rejection Rules

A submission is rejected when:

- `sensitivity == "secret"`.
- content is empty and there are no files.
- title, summary, content, payload, file count, individual file size, or total bundle size exceeds the deterministic governance limits.
- file paths are unsafe, including absolute paths, traversal paths, duplicate normalized paths, `.env*`, private-key names, credential files, or auth/secret JSON files.
- content/payload/files contain secret patterns, including private-key blocks, AWS keys, GitHub tokens, Slack tokens, OpenAI-style `sk-*` tokens, env-style secret assignments, credential-bearing database URLs, or high-entropy token-like strings.
- `unit_type == "skill"` but no uploaded file has path `SKILL.md`.
- `unit_type == "skill"` and `SKILL.md` does not contain frontmatter `name` and `description`.

Rejected submissions remain in `evolution_unit_submission` with `status='rejected'` and a `reject_reason`.

### Needs Review Rules

**Skill only.** Memory-like units never enter `needs_review`.

A **skill** submission moves to `status='needs_review'` when deterministic hard checks pass and either its source requires human review or confidence is not high enough for automatic promotion:

- `evidence.requires_human_review == true` (including L3 memory-curation skill drafts), regardless of LLM reviewer configuration.
- `confidence != "high"`.
- `sensitivity` is not `none` or `local_path`.

When LLM review is enabled, the reviewer may also route submissions to `needs_review` or `rejected` based on structured JSON output. The reviewer is not invoked for candidates that already require human review, and LLM cannot bypass secret/path/size/frontmatter hard gates.

Review queue rows are handled through workspace evolution review APIs and the Settings UI (`EvolutionReviewSection`). They are not promoted until an admin promotes them from review.

### Promotion Rules

**Memory-like units** promote immediately after hard gates pass (no confidence / LLM / manual review).

**Skill** submissions are eligible for automatic promotion (when review is disabled and confidence gates pass) when:

- `confidence == "high"`.
- `sensitivity` is `none` or `local_path`.
- a dedupe hash exists.

Dedupe hash selection:

- `skill` / `workflow` use `bundle_hash`.
- `memory` / `preference` / `tool_pattern` use `content_hash`.

If an existing shared unit with the same dedupe hash exists, the submission is marked promoted and linked to that unit. Otherwise, a new shared unit and version are created.

## Database Writes: Governed Shared Units

### `shared_evolution_unit`

A promoted candidate becomes a governed reusable unit.

Important fields:

- `workspace_id`
- `unit_type`
- `title`
- `canonical_summary`
- `content`
- `metadata` — includes `dedupe_hash`, `content_hash`, `bundle_hash`, `source_agent_id`, and `local_unit_id`.
- `applies`, `tags`, `tools`, `task_types`, `project_types`, `languages`, `frameworks`
- `scope`
- `score`
- `status`
- `current_version_id`

### `shared_evolution_unit_version`

A promoted unit gets version `1` on first creation.

Important fields:

- `unit_id`
- `version`
- `title`
- `content`
- `metadata`
- `applies`
- `source_submission_ids`
- `change_reason`

### `shared_evolution_unit_file`

Files from skill/workflow submissions are copied from `evolution_unit_submission_file` into `shared_evolution_unit_file` for the current version.

Important fields:

- `unit_id`
- `version_id`
- `path`
- `content`
- `content_hash`
- `mime_type`
- `size_bytes`

## Skill Materialization (promotion → public catalog)

When a promoted unit has `unit_type == "skill"`, `EvolutionService.finalizeSkillPromotion` calls `MaterializePromotedSkill` in `server/internal/service/evolution_skill_catalog.go`.

This writes or updates workspace rows in:

```text
skill
skill_file
```

Important behavior:

- `skill.source_evolution_unit_id` links back to `shared_evolution_unit.id` for provenance.
- Only evolution-promoted skills participate in agent suggestion scans (`source_evolution_unit_id IS NOT NULL`).
- Skills imported via `~/.pi/share/skills` or manual workspace CRUD do not set this column and are outside the evolution suggestion matcher.
- `skill.created_by` is resolved from `evolution_unit_submission.source_member_id` → `member.user_id` (user FK), not the member PK.

After materialization, the service binds the skill to the submitting source agent (`agent_skill`, `source='evolution'`), then runs `RefreshWorkspaceAgentSkillSuggestions` for all non-archived agents in the workspace.

## Skill Version Inspection, Eval, and Rollback

Workspace owners and admins can inspect and roll back existing versions of an evolution-promoted skill:

```http
GET  /api/evolution/units/{unitId}/versions?workspace_id={workspaceId}
GET  /api/evolution/units/{unitId}/versions/{versionId}?workspace_id={workspaceId}
GET  /api/evolution/units/{unitId}/versions/{versionId}/eval?workspace_id={workspaceId}
POST /api/evolution/units/{unitId}/versions/{versionId}/rollback?workspace_id={workspaceId}
```

The rollback request must include the version observed by the administrator:

```json
{
  "expected_current_version_id": "current-version-uuid"
}
```

Rollback behavior:

- Both the unit and target version must belong to the requested workspace, the target version must belong to that unit, and the unit must have `unit_type='skill'`.
- The unit row is locked with `SELECT ... FOR UPDATE`; a different current version produces `409 Conflict`. Retrying a completed rollback to the same target is a no-op only when the unit, linked skill content, and supporting files all still match that version. Drift is transactionally repaired and audited.
- New promotion versions store a controlled `metadata.matcher_snapshot` containing `canonical_summary`, `tags`, `tools`, `task_types`, `project_types`, `languages`, and `frameworks`. Rollback restores all of these fields; legacy or malformed versions without a complete snapshot fail closed with `409 Conflict` before any state changes.
- The target must already contain a versioned `SKILL.md`. The transaction updates `shared_evolution_unit.current_version_id` and versioned unit fields, replaces the linked `skill.content` / `skill.description` and `skill_file` rows, refreshes pending agent-skill suggestions, and writes `activity_log.action='evolution_skill_version_rolled_back'`.
- Existing `agent_skill` assignments are not auto-approved, auto-removed, or auto-enabled by rollback. Only pending suggestions are recalculated.
- Any failure in materialization, suggestion refresh, or audit logging rolls back the entire database transaction.

Production task claim records an `injected` feedback event for each evolution-backed skill with the exact current `shared_evolution_unit_version.id`; terminal completion/failure events copy that same version attribution. Eval summaries use existing `evolution_unit_feedback_event` rows only. Events with `metadata.version_id` equal to the requested version form the preferred basis. If none are explicitly attributed, the response labels the result `unit_lifetime_fallback`, reports the unit-lifetime counts, and explains how many events cannot be assigned to a version. Success rate is `success / (success + failure)` and usage rate is `used / injected`; no external model is called.

The implementation uses the existing version, file, feedback, skill, suggestion, assignment, and activity tables. It adds no schema migration.

## Agent Skill Suggestions

Promotion **auto-binds the source agent** that uploaded the candidate. The matcher creates `agent_skill_suggestion` rows for **other** agents only.

### Trigger timing

- After skill materialization (workspace-wide rescan).
- When an agent is created or updated (name, description, instructions, runtime, model, and other profile fields that affect matching).

### Matching scope

- Only `skill` rows with `source_evolution_unit_id IS NOT NULL`.
- Hybrid metadata + semantic scoring (tags, tools, languages, frameworks, task_types, etc.).
- The submitting source agent is skipped by the matcher (already bound directly on promotion).

### Suggestion rules

| Suggestion | Condition |
|------------|-----------|
| `add` | Skill matches agent profile and is not bound in `agent_skill` |
| `remove` | Skill no longer matches and current binding has `agent_skill.source = 'evolution'` |
| (none) | Already bound and still matches; or binding is `manual` / `template` |

Pending suggestions are replaced on each rescan (`DeletePendingAgentSkillSuggestions` then upsert).

### User-facing APIs

```http
GET  /api/agents/{agentId}/skill-suggestions
POST /api/agents/{agentId}/skill-suggestions/{suggestionId}/decision
```

Request body for decision:

```json
{ "decision": "accept" | "dismiss" }
```

- **accept** on `add` → `AddAgentSkillWithSource(source='evolution')`.
- **accept** on `remove` → `RemoveAgentSkill`.
- **dismiss** → marks suggestion dismissed; does not change bindings.

The web/desktop Skills tab renders pending suggestions and calls these endpoints.

## Task Execution (runtime skill injection)

Accepted bindings in `agent_skill` are what the runtime uses. On task dispatch, the handler loads skills via:

```go
TaskService.LoadAgentSkills(ctx, agentID)
```

This reads `skill` + `skill_file` through `agent_skill` joins and returns skill content to the daemon. The daemon injects them into the Pi task environment (for example under `<task_workdir>/.pi/skills/<skill_name>/`).

Evolution-promoted skills therefore reach Pi only after:

1. promotion → `skill` materialization, then
2. matcher → `agent_skill_suggestion`, then
3. user accept → `agent_skill`.

There is no separate enablement step on `skills/enabled/`.

## Memory auto-assign (memory-like units)

For `memory`, `preference`, `tool_pattern`, and `workflow`, the flow after upload is:

```text
1. Deterministic hard gates (secret / path / size / empty content)
2. assignEvolutionMemory → agent_memory (source_agent_id only)
3. Mark submission promoted (no shared_evolution_unit row)
```

Implementation lives in `curateMemorySubmission` / `assignEvolutionMemory` in `server/internal/service/evolution.go`.

Important behavior:

- **No LLM review** and **no `needs_review` queue** for memory-like units, even when `EVOLUTION_REVIEW_ENABLED=true`.
- **No cross-agent assignment** — only the submitting agent (`source_agent_id`) receives the memory row.
- Idempotency via `sync_key`: `evolution/{unit_type}/{local_unit_id}`.
- Re-uploads with the same `local_unit_id` update the existing `agent_memory` row.
- `config.origin` records `evolution_unit_id`, `submission_id`, and `local_unit_id` for traceability.

The agent **Memory** tab reads bound rows via `GET /api/agents/{id}/memories`.

## Relationship To Other Tables

### `skill` / `skill_file` / `agent_skill`

Three distinct entry paths populate the workspace skill catalog:

| Source | How it arrives | `source_evolution_unit_id` | `agent_skill.source` |
|--------|----------------|----------------------------|----------------------|
| `~/.pi/share/skills` scanner | daemon shared-skill sync | NULL | `manual` (default) |
| Manual workspace CRUD | web/desktop settings | NULL | `manual` |
| Evolution promotion | `MaterializePromotedSkill` | set | `evolution` after user accepts suggestion |
| Agent template | template create flow | NULL | `manual` today (template source planned) |

### `agent_memory`

Evolution memory candidates materialize into `agent_memory` for the submitting agent after hard gates pass. The separate runtime sync handler (`SyncAgentMemories`) still exists for direct runtime memory bundles, but the Pi evolution upload path documented here uses `sync_queue/memory-candidates.jsonl` → `evolution_unit_submission` → `agent_memory`.

### `agent_shared_skill`

`agent_shared_skill` tables and sync handler code exist, but those handlers are not wired in the daemon router. The active Pi skill-sharing path outside `~/.pi/share/skills` is the evolution flow documented here.

### Removed: `evolution_unit_delivery`

Migration 137 dropped this table and removed daemon/server delivery endpoints (`GET/POST .../evolution/deliveries*`). Do not reference it in new code or docs.

## End-to-End Summary

```text
1. Pi agent writes a candidate:
   sync_queue/memory-candidates.jsonl
   sync_queue/skill-candidates.jsonl + skills/drafts/<skill_id>/

2. Daemon uploads candidates:
   POST /api/daemon/runtimes/{runtimeId}/evolution/submissions

3. Server writes inbound records:
   evolution_unit_submission
   evolution_unit_submission_file

4. Curator runs deterministic gates:
   memory-like → promoted + agent_memory (source agent)
   skill → optional LLM review → promoted or needs_review or rejected

5. For skill units, server materializes public catalog rows and binds the source agent:
   skill (+ source_evolution_unit_id)
   skill_file
   agent_skill (source agent, source=evolution)

6. Matcher scans other workspace agents and writes suggestions:
   agent_skill_suggestion (add | remove, status=pending)

7. User reviews skill suggestions in agent Skills tab (or via API) — non-source agents only

8. On accept, skill binding is persisted:
   agent_skill (source=evolution for add suggestions)

9. Task dispatch loads bound skills from DB:
   LoadAgentSkills → daemon injects into Pi task environment
```

## Current Limitations

- Raw Pi memory files are not mirrored to the server; only explicit candidate JSONL entries are uploaded.
- Memory-like evolution units auto-bind to the submitting agent only; there is no cross-agent memory suggestion flow.
- Matcher skips the source agent for **skills** (already auto-bound); other agents need sufficient profile metadata or skill suggestions may be empty.
- Workspace rescan after skill promotion is synchronous; large workspaces may need an async job.
- Skill governance defaults to deterministic rules with optional LLM review (`EVOLUTION_REVIEW_ENABLED`); memory-like units never enter that queue.
