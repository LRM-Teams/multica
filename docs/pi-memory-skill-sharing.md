# Pi Memory / Skill Sharing Flow

This note documents the Pi-specific memory and skill sharing paths in Multica, excluding the workspace-level `~/.pi/share/skills` scanner.

Last verified against the current working tree on 2026-06-30.

## Scope

This document covers the Pi agent evolution/governance flow:

```text
Local Pi agent root
  sync_queue/memory-candidates.jsonl
  sync_queue/skill-candidates.jsonl
  skills/drafts/<skill_id>/
        |
        v
Multica server database
  evolution_unit_submission
  evolution_unit_submission_file
        |
        v
Curator / optional LLM advisory review
  shared_evolution_unit
  shared_evolution_unit_version
  shared_evolution_unit_file
        |
        v  (unit_type == skill only)
  skill + skill_file                    # workspace public catalog
        |
        v
  agent_skill_suggestion (add | remove)   # scan only; no auto-bind
        |
        v
  user accept / dismiss (web or desktop UI)
        |
        v
  agent_skill (source: manual | evolution | template)
        |
        v
Task execution
  TaskService.LoadAgentSkills → daemon injects skills into Pi task env
```

It does not cover the global shared-skill scanner under `~/.pi/share/skills`, which syncs directly into workspace `skill` / `skill_file` via the runtime shared-skill sync endpoint.

## Design principles

- **Promotion ≠ assignment.** A promoted skill is written to the workspace public `skill` table. Binding it to an agent is a separate, user-confirmed step.
- **No per-agent delivery table.** `evolution_unit_delivery` was removed (migration 137). The server does not push generated bundles back to `skills/generated/` or `skills/enabled/`.
- **Memory stays in the governance layer for now.** Promoted memory units live in `shared_evolution_unit*` only; there is no automatic per-agent memory assignment yet.

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

`ensurePiAgentRoot` still creates `skills/generated/` and `skills/enabled/` for backward-compatible layout, but the evolution flow documented here does **not** populate them. Agent skills used at task time come from the database via `agent_skill`, not from those directories.

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

When LLM review is disabled (default), a submission moves to `status='needs_review'` when deterministic hard checks pass but confidence is not high enough for automatic promotion:

- `confidence != "high"`.
- `sensitivity` is not `none` or `local_path`.

When LLM review is enabled, the reviewer may also route submissions to `needs_review` or `rejected` based on structured JSON output. LLM cannot bypass secret/path/size/frontmatter hard gates.

Review queue rows are handled through workspace evolution review APIs and the Settings UI (`EvolutionReviewSection`). They are not promoted until an admin promotes them from review.

### Promotion Rules

A submission is eligible for automatic promotion (when review is disabled and confidence gates pass) when:

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

After materialization, the service runs `RefreshWorkspaceAgentSkillSuggestions` for all non-archived agents in the workspace.

## Agent Skill Suggestions

Promotion does **not** bind skills to agents. Instead, the matcher creates rows in `agent_skill_suggestion`.

### Trigger timing

- After skill materialization (workspace-wide rescan).
- When an agent is created or updated (name, description, instructions, runtime, model, and other profile fields that affect matching).

### Matching scope

- Only `skill` rows with `source_evolution_unit_id IS NOT NULL`.
- Hybrid metadata + semantic scoring (tags, tools, languages, frameworks, task_types, etc.).
- The submitting source agent is skipped (no self-suggestion for the agent that uploaded the candidate).

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

## Memory promotion (current state)

Memory, preference, tool_pattern, and workflow units are promoted into `shared_evolution_unit*` the same way as skills, but they are **not** materialized into `agent_memory` and are **not** delivered to `<agent_root>/inbox/memory/` in the current tree.

The active Pi memory upload path ends at governed shared units until a separate memory assignment product is implemented.

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

`agent_memory` tables and sync handler code exist, but per-agent memory assignment is not wired through the evolution flow documented here. Promoted memory units remain in `shared_evolution_unit*`.

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

4. Curator (and optional LLM reviewer) promotes safe candidates:
   shared_evolution_unit
   shared_evolution_unit_version
   shared_evolution_unit_file

5. For skill units, server materializes public catalog rows:
   skill (+ source_evolution_unit_id)
   skill_file

6. Matcher scans workspace agents and writes suggestions:
   agent_skill_suggestion (add | remove, status=pending)

7. User reviews suggestions in agent Skills tab (or via API):
   GET  /api/agents/{id}/skill-suggestions
   POST /api/agents/{id}/skill-suggestions/{id}/decision

8. On accept, binding is persisted:
   agent_skill (source=evolution for add suggestions)

9. Task dispatch loads bound skills from DB:
   LoadAgentSkills → daemon injects into Pi task environment
```

## Current Limitations

- Raw Pi memory files are not mirrored to the server; only explicit candidate JSONL entries are uploaded.
- Promoted memory units are not assigned to agents or written to local inbox paths yet.
- Matcher skips the source agent; other agents need sufficient profile metadata or suggestions may be empty.
- Workspace rescan after promotion is synchronous; large workspaces may need an async job.
- Governance defaults to deterministic rules with optional LLM review (`EVOLUTION_REVIEW_ENABLED`); production review policy is still evolving.
- `agent_memory` and `agent_shared_skill` tables exist, but the wired Pi sharing path for skills uses evolution → `skill` → `agent_skill_suggestion` → `agent_skill`.
