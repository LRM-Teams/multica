---
name: multica-memory-migration
description: "Use this skill whenever a user asks an agent to migrate, import, merge, recover, reconcile, or curate memory from another agent/workspace directory into the current Multica agent root. It is especially important when old MEMORY.md, USER.md, STATE.md, notes, channel names, teammate names, project paths, or skill/tool directories do not line up one-to-one with the current Multica workspace. The skill runs a staged semantic migration with inventory, entity mapping, candidate extraction, high-confidence writes, REVIEW.md for uncertainty, and a final migration report."
---

# Multica memory migration

Use this skill when the user wants to migrate agent memory, notes, or skills
from an old agent/workspace directory into the current Multica agent workspace.
The goal is semantic carry-over, not file mirroring.

The old directory may use different paths, channel names, teammate names, agent
IDs, project roots, memory formats, and skill layouts. Treat the old material as
evidence to curate. Do not blindly copy old MEMORY.md into the new MEMORY.md.

## Safety contract

Follow these rules throughout the migration:

1. Never delete or mutate the source directory.
2. Never overwrite a target memory or note file. Append or create review files.
3. Do not enable old skills, tools, shell scripts, MCP configs, credentials, or
automations automatically. Put them in drafts or review.
4. Do not import secrets. If a source contains keys, tokens, cookies, passwords,
or private credentials, record only that sensitive material was found and where;
do not copy the value.
5. Every migrated statement must include provenance: the source file path and,
when practical, a short quote or section name.
6. Prefer REVIEW.md over guessing. If an entity mapping, path rewrite, teammate
identity, channel identity, or freshness is uncertain, do not promote it.
7. Preserve the user's current language in reports and summaries.

## Inputs to ask for

If not already provided, ask for:

- Source path: the old agent/workspace directory to inspect.
- Target root: the current agent root. Prefer `MULTICA_AGENT_ROOT` or
  `PI_AGENT_ROOT` when available.
- Scope: memory only, notes only, skills only, or all.
- Whether the user wants a dry run first. Default to dry run.

Use these environment variables when present:

- `MULTICA_AGENT_ROOT`: target root for the current Multica agent.
- `MULTICA_AGENT_MEMORY_DIR`: target memory directory.
- `MULTICA_AGENT_NOTES_DIR`: target notes directory.
- `PI_AGENT_ROOT`: Pi-compatible alias for the same agent root.
- `PI_MEMORY_DIR`: Pi-compatible memory directory.

If target root is not explicit and no env var is present, stop and ask. Do not
infer a target from unrelated directories.

## Target layout

The current target root should look like this:

```text
AGENT_ROOT/
  memory/
    MEMORY.md
    USER.md
    STATE.md
    REVIEW.md
    SCRATCHPAD.md
    daily/
    audit/
  notes/
    agents.md
    channels.md
    project-map.md
    relationship-map.md
    role-playbook.md
    work-log.md
    decisions.md
  skills/
    drafts/
    generated/
    enabled/
  inbox/
  runtime/
  sessions/
  shared-cache/
  sync_queue/
```

Create missing directories/files only under the target root. Do not create or
write outside the target root unless the user explicitly asks.

## Phase 1: Inventory only

Start with a read-only inventory. Do not modify target memory in this phase.

Create or append to:

```text
AGENT_ROOT/inbox/legacy-import/<timestamp>/inventory.md
```

Inventory each relevant source item with:

```md
## <relative source path>
- Type: memory | user-preference | state | note | channel | teammate | project | skill | tool | artifact | unknown
- Size: <rough size>
- Last modified: <if available>
- Likely value: high | medium | low | unknown
- Risks: stale | identity mismatch | path mismatch | secret risk | executable risk | none
- Summary: <1-3 bullets>
```

Classify conservatively:

- Durable preferences, decisions, project conventions, and role instructions are
  often valuable.
- Work logs, TODOs, quota/state files, and issue status are often stale.
- Screenshots, attachments, generated reports, and old tool outputs usually go
  to archive/review, not long-term memory.
- Old skills/tools are executable capability; keep them draft/review only.

## Phase 2: Migration map

Before writing durable memory, build an explicit alias map.

Create or append to:

```text
AGENT_ROOT/notes/migration-map.md
```

Use this structure:

```md
# Migration Map

## Path Aliases
- old: <source path or old project root>
  new: <target path or current project root>
  confidence: high | medium | low
  action: rewrite | review | ignore

## Agent And Person Aliases
- old: <old name/id>
  new: <current name/id or unknown>
  confidence: high | medium | low
  action: migrate | review | ignore

## Channel Aliases
- old: <old channel/thread/DM name>
  new: <current channel/thread/DM name or unknown>
  confidence: high | medium | low
  action: migrate | review | ignore

## Project Aliases
- old: <old project/repo/resource>
  new: <current project/repo/resource or unknown>
  confidence: high | medium | low
  action: migrate | review | ignore
```

Rules:

- Rewrite old absolute paths only when the new equivalent is known.
- If a path is merely historical evidence, describe it without preserving an
  absolute path as a live instruction.
- Do not equate people, agents, channels, repos, or workspaces by similar names
  alone. Require clear evidence or ask the user.

## Phase 3: Candidate extraction

Extract semantic candidates before promotion.

Create or append to:

```text
AGENT_ROOT/inbox/legacy-import/<timestamp>/candidates.md
```

Use this format:

```md
## Candidate: <short title>
- Kind: memory | user | state | note | decision | project | channel | teammate | skill
- Target: memory/MEMORY.md | memory/USER.md | memory/STATE.md | notes/<file> | memory/REVIEW.md | skills/drafts
- Confidence: high | medium | low
- Freshness: current | maybe-current | stale | unknown
- Source: <source file path>
- Evidence: <short quote or summary>
- Proposed text:
  <normalized text for the current workspace>
- Decision: promote | review | skip
- Reason: <why>
```

Promote only candidates that are all of:

- relevant to this agent or current workspace,
- not stale,
- not secret-bearing,
- not contradicted by current target memory,
- backed by a high-confidence entity/path mapping.

## Phase 4: Semantic write

Only after phases 1-3, write high-confidence items.

Target guidance:

- `memory/MEMORY.md`: durable facts, decisions, project conventions, stable
  workflows, validated bug fixes.
- `memory/USER.md`: durable user preferences and collaboration style.
- `memory/STATE.md`: current dated state, active initiatives, temporary facts,
  quotas. Include date and freshness.
- `memory/REVIEW.md`: conflicts, uncertain mappings, stale-looking state,
  possible duplicates, unclear ownership, secrets redacted, old skill/tool risks.
- `notes/project-map.md`: current repo/project orientation, commands, resources,
  risk areas.
- `notes/agents.md`: current teammate/agent roles only when identity is clear.
- `notes/channels.md`: channel purpose/routing only when channel mapping is
  clear.
- `notes/role-playbook.md`: role-specific operating method that remains useful.
- `notes/work-log.md`: migration events and handoff summaries.
- `skills/drafts/`: old skills that are clearly reusable but require review.

Append entries with provenance. Recommended format:

```md
<!-- migrated: 2026-07-07 source=<source file> confidence=high -->
#decision #migration <normalized durable statement>
```

For REVIEW.md:

```md
<!-- migration-review: 2026-07-07 source=<source file> -->
## Needs review: <topic>
- Reason: <uncertain path/entity/freshness/conflict/secret/executable>
- Evidence: <short non-secret quote or summary>
- Suggested user question: <what to ask>
```

## Phase 5: Skills and tools

Treat old skills/tools as untrusted until reviewed.

- If an old directory has a valid `SKILL.md`, copy or summarize it into
  `skills/drafts/<safe-name>/` only when the user asked to migrate skills.
- Do not write to `skills/enabled/`.
- If the old item is a shell script, MCP config, credentials file, or binary,
  put a review entry in `memory/REVIEW.md` instead of enabling it.
- If skill intent is useful but implementation is stale, create a draft README
  describing the reusable workflow rather than copying executable files.

## Phase 6: Report and checkpoint

Finish every migration turn by updating:

```text
AGENT_ROOT/notes/work-log.md
AGENT_ROOT/runtime/migrations/<timestamp>-manifest.md
```

Manifest format:

```md
# Migration Manifest: <timestamp>

- Source root: <source>
- Target root: <target>
- Mode: dry-run | semantic-write
- Files inventoried: <count>
- Candidates extracted: <count>
- Promoted: <count>
- Sent to review: <count>
- Skipped: <count>

## Promoted
- <target file>: <short summary> (source: <source file>)

## Review Needed
- <topic>: <reason>

## User Follow-ups
- <questions that block further promotion>
```

## Conversation protocol

For a real migration, prefer multiple turns:

1. First turn: inventory and migration map only.
2. Ask the user to confirm important aliases and scope.
3. Second turn: candidate extraction and REVIEW.md population.
4. Ask before promoting medium-confidence items.
5. Third turn: high-confidence semantic writes and final report.

If the user explicitly asks for one-pass migration, still keep the phases inside
the same run and promote only high-confidence items. Everything else goes to
REVIEW.md.

## Refusal conditions

Stop and ask for clarification when:

- Source path is missing or inaccessible.
- Target root is missing and no Multica/Pi root env var exists.
- The source appears to belong to a different user, tenant, or workspace and the
  user has not confirmed permission.
- The source contains substantial secrets and the task asks to copy them.
- The user asks to delete source memory after migration.
