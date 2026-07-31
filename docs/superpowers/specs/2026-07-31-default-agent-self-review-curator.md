# Default Agent Self-Review And Workspace Curator

- Date: 2026-07-31
- Target branch: `dev`
- Status: implementation in progress

## Problem

Memory self-review currently depends on `memory_curator_profile`. That makes
each workspace/user configure runtime, curator agent, target agents, and feature
switches before nightly self-evolution starts. The product intent is different:
self-review should be system scheduled for active agents by default, while the
workspace curator remains a single governance agent/runtime.

## Target Behavior

1. Nightly `agent_self_review` is automatic.
   - The scheduler scans active agents in each workspace.
   - Activity comes from `agent_inbox_event` and legacy `agent_task_queue`.
   - Each target agent gets one `memory_curation_agent_run` child run.
   - The child run is bound to that agent's own runtime.
   - Runtime offline or provider/cost/network failure is recorded on that child
     run and must not block other agents.

2. `team_curation` is workspace governance.
   - A workspace may configure one curator profile to choose the curator agent,
     curator runtime, mode, confidence threshold, and schedule.
   - The curator consumes self-review candidates and necessary scoped context.
   - It does not impersonate every agent's self-review.

3. Manual and backfill flows remain compatible.
   - Owner/admin manual runs may still target selected agents.
   - Existing profile-backed scheduled runs remain valid for team curation.
   - Old profile-backed self-review is not the product default.

## Phase 1 Scope

Implement default scheduled self-review without requiring profile rows:

- Add a default scheduler path for `agent_self_review`.
- Create one workspace parent `memory_curation_run` per Beijing plan day.
- Create child `memory_curation_agent_run` rows for active agents.
- Include legacy `agent_task_queue` activity in active-agent detection.
- Preserve profile-backed `team_curation` scheduling.

Out of scope for this phase:

- UI copy/config cleanup.
- Narrowing team curation input strictly to candidates.
- New DB constraints for default scheduled run uniqueness.
- New partial-success status values beyond existing `succeeded` / `failed`.

## Data Rules

Default parent run:

- `profile_id = NULL`
- `owner_user_id = NULL`
- `runtime_id = NULL`
- `curator_agent_id = NULL`
- `curator_mode = 'auto_safe'`
- `confidence_threshold = 0.8`
- `target_agent_ids = active agent ids`

Child run:

- `agent_id = target agent`
- `runtime_id = target agent runtime_id`
- `status = queued` when runtime is online, otherwise `waiting_runtime`

Parent dedupe:

- Since there is no unique DB index for profile-less scheduled rows yet, the
  scheduler uses `NOT EXISTS` on `(workspace_id, stage, trigger_kind, date_from,
  profile_id IS NULL)` in this phase.

## Acceptance Tests

- With no `memory_curator_profile`, active agents receive self-review child runs.
- Legacy `agent_task_queue` activity counts as active.
- Inactive agents are skipped.
- Offline active agents create `waiting_runtime` child rows without blocking
  online active agents.
- Profile-backed `team_curation` still creates scheduled runs as before.
