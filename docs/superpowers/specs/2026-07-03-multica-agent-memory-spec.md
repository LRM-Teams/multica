# Multica Agent Memory Spec

Created: 2026-07-03
Status: Draft
Scope: Multica agent-local long-term memory, notes, project maps, and Pi-memory compatibility.

## Goal

Give every Multica agent persistent, file-backed memory comparable to Pi memory while preserving Multica-specific collaboration context:

- Agent personal memory: stable role, preferences, durable facts, current focus.
- Daily logs: append-only session/task handoffs.
- Review-first learning: most candidates enter review and curator promotes only high-value facts.
- Channel notes: lightweight routing/collaboration map for channels and DMs.
- Agent notes: known teammates, responsibilities, boundaries, and collaboration rules.
- Project maps: concise per-project working maps and repo command notes.
- Background curator: local daemon processes dirty agent roots and compacts/promotes memory.

## Non-Goals

- Do not inject full memory, daily logs, review queues, or all project maps into every task prompt.
- Do not solve concurrent file-write locking in the first version.
- Do not make Pi the authority for Multica platform context.
- Do not store chat history transcripts in notes files; Multica DB remains the source of message history.

## Pi Change Decision

No Pi core change is required for v1.

Pi memory already supports the needed file model when Multica sets the right environment variables:

- `PI_AGENT_ROOT`
- `PI_MEMORY_DIR`
- `PI_SKILL_DRAFTS_DIR`
- `PI_AGENT_INBOX_DIR`
- `PI_AGENT_SHARED_CACHE_DIR`
- `PI_AGENT_PROFILE_DIR`
- `PI_AGENT_FEEDBACK_DIR`
- `PI_AGENT_SYNC_QUEUE_DIR`

Multica should provide an agent root whose layout is compatible with Pi memory. Pi then reads/writes the same `MEMORY.md`, `daily/`, `REVIEW.md`, curator state, skill drafts, inbox, shared cache, feedback, and sync queue. Future Pi changes are optional only if we want nicer first-class awareness of Multica notes/project-map files.

## Agent Root Layout

Preferred provider-neutral path:

```text
<workspaces_root>/<workspace_id>/agents/<agent_id>/
```

Compatibility path acceptable for v1 if less invasive:

```text
<workspaces_root>/<workspace_id>/.pi/agents/<agent_id>/
```

Required layout:

```text
<agent_root>/
  MEMORY.md
  USER.md
  STATE.md
  REVIEW.md
  SCRATCHPAD.md
  daily/
    YYYY-MM-DD.md
  audit/
    curator.jsonl
  notes/
    channels.md
    agents.md
    project-map.md
    work-log.md
  projects/
    <project_id>/
      project-map.md
      decisions.md
      repos.md
      work-log.md
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
    feedback.jsonl
  sync_queue/
    memory-candidates.jsonl
    skill-candidates.jsonl
  repos/
```

## Memory Ownership Rules

`MEMORY.md` is shared by Multica and Pi memory. It must stay concise and stable.

Direct writes to `MEMORY.md` are allowed only for:

- Agent role or responsibility changes.
- Explicit user instruction to remember something.
- Durable collaboration rules.
- Facts promoted by curator/reviewer.

Default writes go elsewhere:

- Task/session details -> `daily/YYYY-MM-DD.md`.
- Candidate facts -> `REVIEW.md` or `sync_queue/memory-candidates.jsonl`.
- Channel/DM routing -> `notes/channels.md`.
- Teammate roles -> `notes/agents.md`.
- Project structure/commands -> `projects/<project_id>/project-map.md`.

Use managed markers where Multica updates platform-owned sections:

```md
<!-- multica-managed:start -->
...
<!-- multica-managed:end -->

<!-- agent-maintained:start -->
...
<!-- agent-maintained:end -->
```

Multica may overwrite only `multica-managed` blocks. Agent and curator should avoid overwriting managed blocks.

## File Size Targets

- `MEMORY.md`: 2-8 KB target, compact above 12 KB.
- `notes/channels.md`: 3-8 lines per channel/DM.
- `notes/agents.md`: 3-6 lines per known agent.
- `projects/<project_id>/project-map.md`: 5-15 KB target, compact above 20 KB.
- `daily/*.md`: append-only, may be large, not injected by default.
- `REVIEW.md`: may be large, curator/reviewer only, not injected by default.

## Prompt Injection Policy

Default prompt injects a small snapshot plus paths, not full files.

Inject:

- Agent memory snapshot from `MEMORY.md` core sections, 1-2 KB.
- Current channel/DM entry from `notes/channels.md`, up to 1 KB.
- Current project map summary/top section, 2-4 KB.
- File index with paths to full memory/notes/project files.

Do not inject by default:

- Full `daily/`.
- Full `REVIEW.md`.
- Full `notes/channels.md`.
- Full `notes/agents.md`.
- All project maps.
- Historical chat messages beyond current bounded surface.

Example prompt block:

```text
## Agent Memory Snapshot
- Role: ...
- Stable rules: ...
- Current focus: ...

## Current Surface
- Channel/DM: ...
- Participants: ...
- Norms: ...

## Current Project
- Project: ...
- Repos: ...
- Commands: ...
- Important paths: ...

## Available Local Memory
- Full memory: $MULTICA_AGENT_ROOT/MEMORY.md
- Daily logs: $MULTICA_AGENT_ROOT/daily/
- Channel notes: $MULTICA_AGENT_ROOT/notes/channels.md
- Agent notes: $MULTICA_AGENT_ROOT/notes/agents.md
- Project map: $MULTICA_AGENT_ROOT/projects/<project_id>/project-map.md
```

## Runtime Environment

All providers receive:

```text
MULTICA_AGENT_ROOT=<agent_root>
MULTICA_MEMORY_DIR=<agent_root>
MULTICA_NOTES_DIR=<agent_root>/notes
MULTICA_PROJECT_MEMORY_DIR=<agent_root>/projects/<project_id>
```

Pi provider additionally receives:

```text
PI_AGENT_ROOT=<agent_root>
PI_MEMORY_DIR=<agent_root>
PI_SKILL_DRAFTS_DIR=<agent_root>/skills/drafts
PI_AGENT_INBOX_DIR=<agent_root>/inbox
PI_AGENT_SHARED_CACHE_DIR=<agent_root>/shared-cache
PI_AGENT_PROFILE_DIR=<agent_root>/profile
PI_AGENT_FEEDBACK_DIR=<agent_root>/feedback
PI_AGENT_SYNC_QUEUE_DIR=<agent_root>/sync_queue
```

## Platform Notes Sync

On task preparation, Multica should ensure/update platform-owned summaries:

- `notes/channels.md`: current channel/DM, purpose, project binding, members/agents summary, language/default norms when known.
- `notes/agents.md`: visible teammates, roles, squads, recent collaborators.
- `projects/<project_id>/project-map.md`: project title, resources, repos, local_directory binding if known.

These updates should be bounded and marker-based.

## Agent Workflow Rules

At task start:

1. Read the injected snapshot.
2. If task requires deeper context, read the referenced full file(s) on demand.
3. For project/code work, inspect the repo/workspace and update the project map when useful.

At task end:

1. Append a concise daily handoff.
2. Add review candidates for durable facts instead of directly growing `MEMORY.md`.
3. Update current channel/agent/project notes if the task changed routing or project understanding.

## Curator Model

V1 local curator:

- Multica daemon marks an agent root dirty after task completion or platform notes update.
- A daemon-local curator manager scans dirty roots periodically.
- If Pi memory curator is available, run it against `PI_MEMORY_DIR=<agent_root>`.
- Curator promotes only stable facts to `MEMORY.md`, compacts oversized files, and keeps ambiguous facts in `REVIEW.md`.

Future server/team curator:

- Upload governed candidates, profiles, and feedback from `sync_queue/`, `profile/`, and `feedback/`.
- Server aggregates team-level reusable memory/skills.
- Deliveries are written to `inbox/`, `shared-cache/`, or `skills/generated/`; never overwrite formal memory directly.

## Implementation Plan

1. Add provider-neutral agent root helpers in Multica daemon.
2. Extend agent root initialization to create Pi-compatible memory files plus Multica `notes/`, `projects/`, and `repos/` directories.
3. Inject `MULTICA_AGENT_ROOT` for every provider and Pi memory env vars for Pi provider.
4. Add bounded memory snapshot and path index to `execenv` runtime config.
5. Add platform-owned notes sync for current channel/DM, agents, and project metadata.
6. Add daemon dirty-root tracking and local curator manager.
7. Add optional CLI helpers for agent memory inspection and manual curate.

## First Version Acceptance Criteria

- A newly created/running agent has an initialized root with `MEMORY.md`, `daily/`, `REVIEW.md`, `notes/`, `projects/`, and Pi memory subdirectories.
- A Pi-backed Multica agent uses that root for `memory_write`, `memory_read`, daily logs, curator, and skill drafts.
- Non-Pi providers receive the same memory paths in prompt/env and can read/write files manually.
- Default task prompt stays bounded and does not include full daily/review/channel/project history.
- Current channel/DM and project context produce bounded notes files.
- After a task, daily handoff and review-first candidate workflow work locally.
