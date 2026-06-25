# Pi Memory / Skill Sharing Flow

This note documents the Pi-specific memory and skill sharing paths in Multica, excluding the workspace-level `~/.pi/share/skills` scanner.

Last verified against the current working tree on 2026-06-25.

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
Curator / matcher
  shared_evolution_unit
  shared_evolution_unit_version
  shared_evolution_unit_file
  evolution_unit_delivery
        |
        v
Local Pi agent root
  inbox/memory/<unit_id>.md
  skills/generated/<unit_id>/
```

It does not cover the global shared-skill scanner under `~/.pi/share/skills`, which syncs directly into workspace skills.

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
  generated/
  enabled/
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

The daemon exposes the paths to the Pi runtime through environment variables:

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

These paths are created for Pi runtime state or downflow, but are not direct upload sources in the current implementation:

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

If local Pi memory should be shared, Pi must emit a governed candidate into `sync_queue/memory-candidates.jsonl`. Multica does not mirror the raw memory files as a folder sync.

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
- `status` — starts as `candidate`, may become `rejected` or `promoted`.
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

This performs a deterministic MVP governance pass.

### Rejection Rules

A submission is rejected when:

- `sensitivity == "secret"`.
- content/payload/files contain obvious secret patterns, such as `BEGIN PRIVATE KEY`, `AKIA`, `xoxb-`, `ghp_`, `database_url=`, `npm_token`, or `sk-`.
- content is empty and there are no files.
- `unit_type == "skill"` but no uploaded file has path `SKILL.md`.

Rejected submissions remain in `evolution_unit_submission` with `status='rejected'` and a `reject_reason`.

### Promotion Rules

A submission is eligible for promotion when:

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

## Matcher and Delivery Queue

After promotion, the MVP matcher currently matches only the source agent that submitted the unit.

It creates a row in:

```text
evolution_unit_delivery
```

Delivery type:

- `skill` -> `generated`
- `memory`, `preference`, `tool_pattern`, `workflow` -> `inbox`

Important fields:

- `workspace_id`
- `unit_id`
- `version_id`
- `target_agent_id`
- `delivery_type`
- `status` — starts as `pending`.
- `reason`
- `matcher_score`
- `matcher_details`
- `delivered_path`
- `error`

## Downflow Back To Pi

The daemon has a Pi-only delivery loop. It polls pending deliveries through:

```http
GET /api/daemon/runtimes/{runtimeId}/evolution/deliveries?agent_id=<agent_id>
```

The server returns active pending deliveries plus shared unit metadata and files.

### Skill Delivery

For `unit_type == "skill"`, the daemon writes files to:

```text
<agent_root>/skills/generated/<unit_id>/
```

It also writes metadata:

```text
<agent_root>/skills/generated/<unit_id>/.multica-delivery.json
```

The metadata currently contains:

```json
{
  "delivery_id": "...",
  "unit_id": "...",
  "version_id": "...",
  "enabled": false
}
```

Generated skills are therefore delivered but not automatically enabled.

### Memory Delivery

For `memory`, `preference`, `tool_pattern`, and `workflow`, the daemon writes a markdown inbox item:

```text
<agent_root>/inbox/memory/<unit_id>.md
```

The file includes frontmatter with delivery metadata and the unit content.

### Delivery Status Updates

After local write succeeds, the daemon marks delivery as delivered:

```http
POST /api/daemon/runtimes/{runtimeId}/evolution/deliveries/{deliveryId}/delivered?agent_id=<agent_id>
```

The server updates `evolution_unit_delivery.status` to `delivered` and records `delivered_path`.

If local write fails, the daemon calls:

```http
POST /api/daemon/runtimes/{runtimeId}/evolution/deliveries/{deliveryId}/failed?agent_id=<agent_id>
```

The server updates the delivery to `failed` and records the error.

There is also a decision endpoint:

```http
POST /api/daemon/runtimes/{runtimeId}/evolution/deliveries/{deliveryId}/decision?agent_id=<agent_id>
```

Valid decisions are `accepted`, `ignored`, and `rejected`.

## Relationship To Other Tables

### `skill` / `skill_file`

The excluded `~/.pi/share/skills` scanner writes into the workspace skill tables (`skill` and `skill_file`) via the runtime shared-skill sync endpoint.

The evolution/governance flow in this document does not write generated skill candidates directly into `skill` / `skill_file`. It writes them into evolution tables first, then delivers generated files back to Pi agent roots.

### `agent_memory`

The repository contains `agent_memory` tables and sync handler code, but those handlers are not wired in the daemon router in the current tree. The active Pi memory-sharing path is `evolution_unit_submission` -> `shared_evolution_unit` -> `evolution_unit_delivery`.

### `agent_shared_skill`

The repository contains `agent_shared_skill` tables and sync handler code, but those handlers are not wired in the daemon router in the current tree. The active Pi skill-sharing path outside `~/.pi/share/skills` is the evolution/governance flow documented here.

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

4. Curator promotes safe/high-confidence candidates:
   shared_evolution_unit
   shared_evolution_unit_version
   shared_evolution_unit_file

5. Matcher queues delivery to the source agent:
   evolution_unit_delivery

6. Daemon pulls pending deliveries:
   GET /api/daemon/runtimes/{runtimeId}/evolution/deliveries

7. Daemon writes local downflow:
   skills/generated/<unit_id>/       for skills
   inbox/memory/<unit_id>.md         for memory-like units

8. Daemon marks delivery state:
   delivered / failed / accepted / ignored / rejected
```

## Current Limitations

- Raw Pi memory files are not mirrored to the server; only explicit candidate JSONL entries are uploaded.
- Matcher currently targets only the source agent, not all potentially relevant agents in the workspace.
- Generated skills are delivered with `enabled: false`; there is no automatic enablement in this flow.
- `agent_memory` and `agent_shared_skill` tables exist, but the currently wired Pi sharing path uses the evolution tables.
- Governance is deterministic MVP logic, not an LLM curator.
