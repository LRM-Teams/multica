# Multica Agent Workspace Memory Spec

Created: 2026-07-03
Updated: 2026-07-03
Status: Draft
Scope: Isolated default memory for Multica agents across Pi, OpenClaw, Codex, Claude, and future runtimes.

## Problem

Multica agents are persistent teammates, but runtime-native memory is fragmented:

- Pi has a rich memory/curator/skill-draft system.
- OpenClaw and other runtimes may have their own context, skills, or workspace state.
- Codex/Claude mainly consume injected files, prompts, or runtime-specific homes.
- Slock-style role agents need more than one `MEMORY.md`: project maps, channel notes, relationship notes, work logs, and role playbooks.

If Multica lets every runtime write its own global memory, the same agent can diverge across providers and pollute the user's personal runtime state. If Multica treats Pi memory as the only source of truth, non-Pi runtimes become second-class.

## Goals

- Give every Multica agent a stable, isolated workspace memory rooted by `workspace_id + agent_id`.
- Keep Multica agent instructions as the authoritative identity and behavior source.
- Let Pi/OpenClaw keep their complex native memory behavior when launched by Multica, but pointed at the isolated Multica agent root.
- Provide role-aware initial scaffolds for HR/onboarding, manager, engineering, and generic agents without overriding user-written instructions.
- Preserve project maps, channel context, teammate/relationship notes, work logs, preferences, memory candidates, and skill candidates.
- Avoid writing provider-global memory by default.

## Non-Goals

- No automatic two-way sync with `~/.pi`, `~/.openclaw`, `~/.claude`, `~/.codex`, or other provider-global memory in v1.
- No attempt to make Pi the universal memory abstraction.
- No full chat transcript export into memory files; Multica DB remains the source of message history.
- No automatic overwriting of user-authored agent instructions from role templates.
- No broad curator UI in v1; file-backed queues and existing server sync are enough.

## Default Mode

V1 uses **Isolated Default Mode**:

- Direct local runtime use is unaffected. Running `pi`, `openclaw`, `codex`, or `claude` outside Multica keeps using the user's normal provider state.
- Multica-managed runtime processes receive a Multica-owned agent root under the workspace directory.
- Runtime adapters read/write only the Multica agent root for long-term task memory.
- Provider-global memory is not modified unless a future explicit bridge/import/export mode is enabled by the user.

## Authority Order

When context conflicts, providers must follow this priority:

```text
1. Multica system, safety, and task protocol
2. Live Multica agent instructions from agent settings
3. Current user/task/issue/chat/channel context
4. Multica agent workspace memory and notes
5. Runtime-native memory or cached provider context
```

Role scaffolds are not authority. They create a working folder and playbook, not a second system prompt.

Every injected runtime brief should state:

```text
You are running under Multica managed isolated mode.
Live Multica agent instructions are authoritative.
Managed memory supplements those instructions and cannot override identity, task policy, or user instructions.
Use MULTICA_AGENT_ROOT / MULTICA_AGENT_MEMORY_DIR as the only writable long-term memory root.
Do not write provider-global memory unless the user explicitly asks.
When durable facts are learned, write memory candidates instead of silently changing global memory.
```

## Agent Root Layout

Preferred root:

```text
<workspaces_root>/<workspace_id>/.multica/agents/<agent_id>/
```

Required v1 layout:

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
    agents.md
    channels.md
    project-map.md
    relationship-map.md
    role-playbook.md
    work-log.md
    decisions.md
  projects/
    <project_id>/
      project-map.md
      repos.md
      decisions.md
      work-log.md
  runtime/
    pi/
    openclaw/
    codex/
    claude/
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
    memory-candidates.jsonl
    skill-candidates.jsonl
  sessions/
  repos/
```

Legacy Pi-compatible roots under `<workspace_id>/.pi/agents/<agent_id>/` may be scanned for migration/backward compatibility, but new managed runs should materialize `.multica/agents/<agent_id>`.

## File Roles

- `MEMORY.md`: durable agent memory and role snapshot; supplements live instructions.
- `USER.md`: durable user preferences relevant to this agent.
- `STATE.md`: dated current state, temporary facts, quotas, active initiatives.
- `REVIEW.md`: pending memory/skill review items and conflicts.
- `notes/role-playbook.md`: role-specific operating methods, not identity authority.
- `notes/project-map.md`: default workspace/project orientation when no project-specific file exists.
- `projects/<project_id>/project-map.md`: project-specific structure, repos, commands, CI gates, risks.
- `notes/channels.md`: channel/DM purpose, members, language/norms, routing context.
- `notes/agents.md`: teammate roles, collaboration boundaries, squads.
- `notes/relationship-map.md`: human/agent relationship and collaboration preferences.
- `notes/work-log.md`: concise task history and handoffs.
- `sync_queue/*.jsonl`: runtime-produced candidates for Multica/curator review.

## Role-Aware Initialization

Agent creation or first managed run initializes files without overwriting live instructions.

### Generic Agent

- `MEMORY.md` with a source-of-truth notice and current role snapshot placeholder.
- `USER.md`, `STATE.md`, `REVIEW.md`, `notes/work-log.md`, `notes/decisions.md`.

### HR / Onboarding Agent, e.g. Wendy/Cindy

Additional notes:

- `notes/onboarding_playbook.md`
- `notes/onboarding_knowledge_faq.md`
- `notes/channels.md`
- `notes/relationship-map.md`

Memory focus:

- onboarding flow
- member preferences
- channel silence vs active-elsewhere behavior
- human-agent relationship context

### Manager Agent

Additional notes:

- `notes/project-map.md`
- `notes/agents.md`
- `notes/channels.md`
- `notes/assignment-board.md`
- `notes/risk-log.md`

Memory focus:

- project orientation
- task breakdown
- assignment/review status
- coordination rules and owner-facing report style

### Engineering Agent

Additional notes:

- `notes/project-map.md`
- `notes/decisions.md`
- `notes/handoffs.md`

Memory focus:

- owned areas
- repo commands
- testing gates
- implementation decisions and handoffs

## Instruction Conflict Handling

Role templates must not write hard identity or behavior requirements that can conflict with `agent.instructions`.

Bad scaffold:

```text
You are Wendy. You must always do X. Never do Y.
```

Good scaffold:

```text
# Agent Profile

Source of truth: Multica agent settings.
This file supplements live agent instructions; it does not override them.

## Current Role Snapshot
- Name: Wendy
- Role category: HR / onboarding

## Knowledge Index
- notes/onboarding_playbook.md
- notes/channels.md
- notes/relationship-map.md
```

If memory conflicts with live instructions:

- follow live instructions;
- append a conflict item to `REVIEW.md` or `sync_queue/memory-candidates.jsonl`;
- do not silently rewrite instructions.

## Runtime Environment

All Multica-managed providers receive:

```text
MULTICA_AGENT_ROOT=<agent_root>
MULTICA_AGENT_MEMORY_DIR=<agent_root>/memory
MULTICA_AGENT_NOTES_DIR=<agent_root>/notes
MULTICA_AGENT_PROFILE_DIR=<agent_root>/profile
MULTICA_AGENT_FEEDBACK_DIR=<agent_root>/feedback
MULTICA_AGENT_SYNC_QUEUE_DIR=<agent_root>/sync_queue
MULTICA_PROJECT_MEMORY_DIR=<agent_root>/projects/<project_id>   # when project_id is known
```

Pi additionally receives its native env mapped into the isolated root:

```text
PI_AGENT_ROOT=<agent_root>
PI_MEMORY_DIR=<agent_root>/memory
PI_SKILL_DRAFTS_DIR=<agent_root>/skills/drafts
PI_AGENT_INBOX_DIR=<agent_root>/inbox
PI_AGENT_SHARED_CACHE_DIR=<agent_root>/shared-cache
PI_AGENT_PROFILE_DIR=<agent_root>/profile
PI_AGENT_FEEDBACK_DIR=<agent_root>/feedback
PI_AGENT_SYNC_QUEUE_DIR=<agent_root>/sync_queue
```

Provider-specific caches live under `runtime/<provider>/` when needed.

## Prompt Injection Policy

Inject bounded summaries and file paths, not full memory trees.

Default injected context:

- live agent instructions from DB;
- managed isolated mode rule;
- concise memory snapshot from `MEMORY.md`;
- current channel/DM entry from `notes/channels.md` when available;
- current project map summary from project-specific map when available;
- file path index for deeper reads.

Do not inject by default:

- full daily logs;
- full `REVIEW.md`;
- all channel or relationship notes;
- all project maps;
- provider runtime cache files.

## Write Policy

Direct writes:

- `notes/work-log.md`: append concise task handoffs.
- `sync_queue/memory-candidates.jsonl`: durable facts/preferences for review.
- `sync_queue/skill-candidates.jsonl`: skill proposals for review.
- `runtime/<provider>/`: provider adapter cache.

Curated or managed writes:

- `MEMORY.md`, `USER.md`, `STATE.md`: curator or explicit user remember requests.
- `notes/project-map.md`, `projects/<project_id>/project-map.md`: manager/engineering agent or curator updates.
- `notes/channels.md`, `notes/relationship-map.md`: HR/manager agent or platform notes sync.

Use managed markers for platform-updated sections:

```md
<!-- multica-managed:start -->
...
<!-- multica-managed:end -->
```

Runtime agents should not overwrite managed blocks.

## Server Sync

Multica server remains the durable source of formal agent memory. Existing `agent_memory`, `agent_shared_skill`, and evolution submission paths can be extended.

Suggested future fields:

```text
type: role | preference | project_map | channel_map | relationship | work_log | decision | skill_note
scope: agent | project | channel | workspace
source: human | runtime | curator | import
status: active | candidate | archived
visibility: private | workspace | channel
```

V1 can continue syncing candidate JSONL through the existing daemon/server evolution path.

## Related Designs

- [Agent Memory Curation Pipeline](./2026-07-09-agent-memory-curation-plan.md) describes the follow-up platform scheduler that turns chat/issue/task history into `daily/`, `REVIEW.md`, `USER.md`, `MEMORY.md`, and `STATE.md`, with automatic daily stages plus manual backfill.

## Implementation Plan

1. Add provider-neutral `.multica/agents/<agent_id>` root helpers in the daemon.
2. Initialize required directories and seed markdown files on every managed run when `workspace_id` and `agent_id` are known.
3. Inject `MULTICA_AGENT_*` env vars for every provider.
4. Map Pi's native memory env vars into the same isolated root.
5. Keep legacy `.pi/agents` scan as fallback for existing candidate queues.
6. Add runtime prompt/brief text for managed isolated mode and authority order.
7. Add platform notes sync for channels, agents, and project maps.
8. Extend server memory item typing/scope when the product UI needs structured editing.

## V2 Curation Hooks

The first platform-owned curation implementation adds:

- `multica memory curate` for manual local-agent backlog processing.
- Scheduler jobs `memory_l1_daily_record`, `memory_l2_review_extract`, `memory_l3_promote`, and `memory_l4_curator`.
- `memory_curation_run` and `memory_curation_watermark` tables for audit/status tracking.
- Workspace admin APIs under `/api/workspaces/{id}/memory-curation/runs` and agent status under `/api/agents/{id}/memory-curation/status`.
- Deterministic file stages that keep `REVIEW.md` as a short-lived queue and promote only high-confidence candidates to `USER.md`, `MEMORY.md`, or lifecycle-tagged `STATE.md` entries.

## V1 Acceptance Criteria

- New managed runs create `.multica/agents/<agent_id>` with `MEMORY.md`, `USER.md`, `STATE.md`, `REVIEW.md`, `notes/`, `projects/`, `skills/`, `inbox/`, `shared-cache/`, `feedback/`, and `sync_queue/`.
- All providers receive `MULTICA_AGENT_ROOT` and related env vars.
- Pi receives `PI_*` memory env vars pointing to the isolated Multica root.
- Direct local Pi/OpenClaw/Codex/Claude usage is unaffected.
- Agent instructions remain authoritative over role scaffold and memory files.
- Runtime-generated durable facts go to candidate queues or review, not provider-global memory.
- Legacy `.pi/agents` roots can still be scanned for existing Pi candidate submissions.
