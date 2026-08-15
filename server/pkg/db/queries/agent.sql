-- name: ListAgents :many
SELECT * FROM agent
WHERE workspace_id = $1 AND archived_at IS NULL
ORDER BY created_at ASC;

-- name: ListAllAgents :many
SELECT * FROM agent
WHERE workspace_id = $1
ORDER BY created_at ASC;

-- name: GetAgent :one
SELECT * FROM agent
WHERE id = $1;

-- name: GetAgentInWorkspace :one
SELECT * FROM agent
WHERE id = $1 AND workspace_id = $2;

-- name: CreateAgent :one
INSERT INTO agent (
    workspace_id, name, display_name, description, avatar_url, avatar_source,
    avatar_attachment_id, runtime_mode, runtime_config, runtime_id,
    max_concurrent_tasks, owner_id, instructions, custom_env, custom_args,
    mcp_config, model, thinking_level
) VALUES (
    $1, $2, $3, $4, sqlc.narg('avatar_url'),
    COALESCE(NULLIF(sqlc.arg('avatar_source')::text, ''), 'assigned'),
    sqlc.narg('avatar_attachment_id'), $5, $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15
)
RETURNING *;

-- name: UpdateAgent :one
UPDATE agent SET
    name = COALESCE(sqlc.narg('name'), name),
    display_name = COALESCE(sqlc.narg('display_name'), display_name),
    description = COALESCE(sqlc.narg('description'), description),
    avatar_url = CASE
        WHEN sqlc.arg('avatar_selection_set')::boolean THEN sqlc.narg('avatar_url')
        ELSE avatar_url
    END,
    avatar_source = CASE
        WHEN sqlc.arg('avatar_selection_set')::boolean THEN sqlc.arg('avatar_source')
        ELSE avatar_source
    END,
    avatar_attachment_id = CASE
        WHEN sqlc.arg('avatar_selection_set')::boolean THEN sqlc.narg('avatar_attachment_id')
        ELSE avatar_attachment_id
    END,
    runtime_config = COALESCE(sqlc.narg('runtime_config'), runtime_config),
    runtime_mode = COALESCE(sqlc.narg('runtime_mode'), runtime_mode),
    runtime_id = COALESCE(sqlc.narg('runtime_id'), runtime_id),
    status = COALESCE(sqlc.narg('status'), status),
    max_concurrent_tasks = COALESCE(sqlc.narg('max_concurrent_tasks'), max_concurrent_tasks),
    instructions = COALESCE(sqlc.narg('instructions'), instructions),
    custom_env = COALESCE(sqlc.narg('custom_env'), custom_env),
    custom_args = COALESCE(sqlc.narg('custom_args'), custom_args),
    mcp_config = COALESCE(sqlc.narg('mcp_config'), mcp_config),
    model = COALESCE(sqlc.narg('model'), model),
    thinking_level = COALESCE(sqlc.narg('thinking_level'), thinking_level),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkAgentRuntimeReassigned :exec
-- Stamped by UpdateAgent's handler when a request actually moves an agent
-- onto a different runtime (never on a no-op "update" that repeats the
-- current runtime_id). Read by EnsureDaemonAgentCredential (task #38) to
-- grant a short grace window where a stale-runtime 403 is reported as a
-- silent, retryable in-progress-transition rather than the terminal
-- agent_reassigned_elsewhere failure from #1628.
UPDATE agent SET runtime_reassigned_at = now()
WHERE id = $1;

-- name: ClearAgentThinkingLevel :one
-- Explicit NULL-clear for thinking_level. COALESCE-based UpdateAgent cannot
-- set the column back to NULL, so the API layer routes "user picked Default"
-- through this dedicated query.
UPDATE agent SET thinking_level = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearAgentMcpConfig :one
UPDATE agent SET mcp_config = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateAgentCustomEnv :one
-- Replaces an agent's custom_env map wholesale. Used by the dedicated
-- env-management endpoint (POST/PUT /api/agents/{id}/env), which is the
-- only post-creation write path for env. UpdateAgent has been stripped
-- of custom_env handling so all env mutations flow through here and the
-- handler's audit-log + **** sentinel guard.
UPDATE agent
SET custom_env = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ArchiveAgent :one
UPDATE agent SET archived_at = now(), archived_by = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ArchiveAgentsByRuntime :many
-- Bulk-archives every active agent bound to any runtime in the given set.
-- Used when revoking a leaving member's runtimes so agents pinned to those
-- runtimes can no longer be assigned new work. Returns the affected rows so
-- the caller can broadcast agent:archived per agent.
UPDATE agent
SET archived_at = now(), archived_by = @archived_by, updated_at = now()
WHERE runtime_id = ANY(@runtime_ids::uuid[]) AND archived_at IS NULL
RETURNING *;

-- name: ArchiveAgentsByIDs :many
-- Narrow archive that only touches the explicit ID list. Used by the
-- cascade-delete endpoint so the user's expected_active_agent_ids list
-- is the authoritative bound on what gets archived: any agent that
-- appeared on the runtime after the user opened the dialog is filtered
-- out here so it can't be silently archived even in the (vanishingly
-- rare) case where a row-level race slips past the runtime FOR UPDATE
-- lock. Returns the affected rows so the caller can broadcast
-- agent:archived per agent.
UPDATE agent
SET archived_at = now(), archived_by = @archived_by, updated_at = now()
WHERE id = ANY(@agent_ids::uuid[]) AND archived_at IS NULL
RETURNING *;

-- name: ListActiveAgentsByRuntime :many
-- Returns every non-archived agent bound to a runtime. Backs the cascade
-- delete dialog: when DELETE /api/runtimes/:id refuses with
-- runtime_has_active_agents, the response carries this list so the front-end
-- can render exactly the agents that will be archived if the user confirms,
-- and so the cascade endpoint's expected_active_agent_ids check has a stable
-- snapshot to compare against. Ordered by name for a deterministic display.
SELECT * FROM agent
WHERE runtime_id = $1 AND archived_at IS NULL
ORDER BY name ASC;

-- name: ListActiveAgentsByRuntimeForUpdate :many
-- FOR UPDATE variant used inside the cascade-delete transaction. Locks
-- each currently-active agent row so a concurrent archive/move of one
-- of those rows blocks until our transaction commits. Pair with
-- LockAgentRuntime, which holds the runtime row exclusively to also
-- block FK-validated INSERTs / runtime_id updates that would otherwise
-- add a new agent to the runtime mid-cascade. Together they guarantee
-- that the set we compared against expected_active_agent_ids is exactly
-- the set ArchiveAgentsByIDs will operate on — no race window.
SELECT * FROM agent
WHERE runtime_id = $1 AND archived_at IS NULL
ORDER BY name ASC
FOR UPDATE;

-- name: ListActiveAgentsByRuntimesForUpdate :many
-- Computer removal locks every active agent across the selected provider
-- runtimes and compares this exact set with the user's confirmation snapshot.
SELECT * FROM agent
WHERE runtime_id = ANY(@runtime_ids::uuid[]) AND archived_at IS NULL
ORDER BY name ASC, id ASC
FOR UPDATE;

-- name: RestoreAgent :one
UPDATE agent SET archived_at = NULL, archived_by = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListAgentTasks :many
SELECT * FROM agent_inbox_event
WHERE agent_id = $1
ORDER BY created_at DESC
LIMIT 50;

-- name: CreateAgentTask :one
INSERT INTO agent_inbox_event (
    workspace_id, agent_session_id, agent_id, runtime_id, execution_config,
    issue_id, reason, requires_wake, status, priority, trigger_comment_id,
    trigger_summary, force_fresh_session, is_leader_task, context
)
SELECT
    a.workspace_id, ensure_agent_wake_session(a.id), a.id,
    sqlc.arg(runtime_id), sqlc.narg(context),
    sqlc.arg(issue_id),
    CASE
      WHEN (sqlc.narg(context)::jsonb)->>'type' = 'environment_dispatch' THEN 'environment_dispatch'
      WHEN (sqlc.narg(context)::jsonb)->>'type' = 'memory_curation' THEN 'memory_curation'
      WHEN (sqlc.narg(context)::jsonb)->>'type' = 'reminder' THEN 'reminder'
      ELSE 'issue'
    END,
    true, 'pending', sqlc.arg(priority), sqlc.narg(trigger_comment_id),
    sqlc.narg(trigger_summary),
    COALESCE(sqlc.narg('force_fresh_session')::boolean, FALSE),
    COALESCE(sqlc.narg('is_leader_task')::boolean, FALSE),
    sqlc.narg(context)
FROM agent a
WHERE a.id = sqlc.arg(agent_id)
RETURNING *;

-- name: CreateQuickCreateTask :one
-- Quick-create tasks have no issue / chat / autopilot link; the entire job
-- description (prompt, requester, workspace) lives in context JSONB. The
-- daemon detects this variant via context.type == "quick_create".
INSERT INTO agent_inbox_event (
  workspace_id, agent_session_id, agent_id, runtime_id, execution_config,
  issue_id, reason, requires_wake, status, priority, context
)
SELECT
  a.workspace_id, ensure_agent_wake_session(a.id), a.id,
  sqlc.arg(runtime_id), sqlc.arg(context),
  NULL, 'quick_create', true, 'pending', sqlc.arg(priority), sqlc.arg(context)
FROM agent a
WHERE a.id = sqlc.arg(agent_id)
RETURNING *;

-- name: LinkTaskToIssue :exec
-- Attaches the issue a quick-create task produced back to the task row, once
-- the agent has finished and the issue exists. Guarded by `issue_id IS NULL`
-- so this never overwrites an issue id that was set at task creation (only
-- quick-create tasks land here unset). Fixes the activity row staying on
-- "Creating issue" forever after completion.
UPDATE agent_inbox_event
SET issue_id = $2
WHERE id = $1 AND issue_id IS NULL;

-- name: SetAgentTaskMaxAttempts :exec
-- Radar owns provider backoff at the workspace scheduler, so its queue row
-- must never also create an automatic retry attempt.
UPDATE agent_inbox_event
SET max_attempts = $2
WHERE id = $1;

-- name: UpsertAgentTaskProgressSnapshot :exec
INSERT INTO agent_task_progress_snapshot (task_id, summary, step, total, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (task_id) DO UPDATE
SET summary = EXCLUDED.summary,
    step = EXCLUDED.step,
    total = EXCLUDED.total,
    updated_at = now();

-- name: CreateRetryTask :one
-- Clones a parent task into a fresh queued attempt. Carries forward the
-- agent's resume context (session_id/work_dir) so the child can continue
-- the conversation when the backend supports it. Resume-unsafe failures are
-- retried as fresh sessions so the child does not inherit a stuck agent
-- conversation. Keep the CASE WHEN predicates in sync with
-- resumeUnsafeFailureReason and the resume lookup blacklists. attempt is
-- incremented; max_attempts, trigger_comment_id, and is_leader_task are
-- inherited so the retried task keeps the same historical provenance as its
-- parent.
INSERT INTO agent_inbox_event (
    workspace_id, agent_session_id, conversation_id, channel_id,
    agent_id, runtime_id, execution_config, issue_id, chat_session_id,
    autopilot_run_id, reason, requires_wake, status, priority,
    trigger_comment_id, trigger_summary, context,
    session_id, work_dir,
    attempt, max_attempts, parent_task_id, force_fresh_session, is_leader_task
)
SELECT
    p.workspace_id, ensure_agent_wake_session(p.agent_id), p.conversation_id, p.channel_id,
    p.agent_id, COALESCE(sqlc.narg('runtime_id')::uuid, p.runtime_id),
    COALESCE(sqlc.narg('context')::jsonb, p.context), p.issue_id,
    p.chat_session_id, p.autopilot_run_id, p.reason, true, 'pending',
    p.priority, p.trigger_comment_id, p.trigger_summary,
    COALESCE(sqlc.narg('context')::jsonb, p.context),
    CASE WHEN p.failure_reason IN ('codex_semantic_inactivity', 'grok_first_turn_no_progress', 'agent_error.context_overflow') THEN NULL ELSE p.session_id END,
    CASE WHEN p.failure_reason IN ('codex_semantic_inactivity', 'grok_first_turn_no_progress', 'agent_error.context_overflow') THEN NULL ELSE p.work_dir END,
    p.attempt + 1, p.max_attempts, p.id,
    COALESCE(p.failure_reason IN ('codex_semantic_inactivity', 'grok_first_turn_no_progress', 'agent_error.context_overflow'), false),
    p.is_leader_task
FROM agent_inbox_event p
WHERE p.id = @id
RETURNING *;

-- name: CancelAgentTasksByIssue :many
-- Cancels every active task on the issue and returns the affected rows so the
-- caller can reconcile each agent's status and broadcast task:cancelled events
-- (#1587). Prior :exec form silently dropped that info, so internal cancel
-- paths (issue status flips to cancelled/done, etc.) left agents stuck at
-- status="working" with no self-correction.
UPDATE agent_inbox_event
SET status = 'suppressed', terminal_outcome = 'cancelled',
    completed_at = now(), terminal_at = now(), acked_at = now()
WHERE issue_id = $1 AND status IN ('pending', 'draining', 'failed')
RETURNING *;

-- name: CancelAgentTasksByIssueAndAgent :many
-- Cancels active tasks for a single (issue, agent) pair without touching
-- tasks belonging to other agents on the same issue. Used by the manual
-- rerun flow so re-running the assignee doesn't collateral-cancel a
-- still-running @-mention agent on the same issue.
UPDATE agent_inbox_event
SET status = 'suppressed', terminal_outcome = 'cancelled',
    completed_at = now(), terminal_at = now(), acked_at = now()
WHERE issue_id = $1 AND agent_id = $2 AND status IN ('pending', 'draining', 'failed')
RETURNING *;

-- name: CancelInFlightTasksByIssueAndAgent :many
-- Cancels only already-claimed/running work for a single (issue, agent) pair.
-- Queued follow-up tasks are deliberately left alone so the daemon can pick up
-- the latest human guidance immediately after interrupting the stale run.
UPDATE agent_inbox_event
SET status = 'suppressed', terminal_outcome = 'cancelled',
    completed_at = now(), terminal_at = now(), acked_at = now(),
    failure_reason = 'followup_interrupt'
WHERE issue_id = $1 AND agent_id = $2 AND id <> $3 AND status = 'draining'
RETURNING *;

-- name: CancelInFlightChatTasksBySessionAndAgent :many
-- Same interrupt path for chat sessions: leave queued follow-up chat turns in
-- place, but stop the currently claimed turn so fresh guidance can run next.
UPDATE agent_inbox_event
SET status = 'suppressed', terminal_outcome = 'cancelled',
    completed_at = now(), terminal_at = now(), acked_at = now(),
    failure_reason = 'followup_interrupt'
WHERE chat_session_id = $1 AND agent_id = $2 AND id <> $3 AND status = 'draining'
RETURNING *;

-- name: CancelAgentTasksByAgent :many
-- Bulk-cancel every active (queued/dispatched/running) task for an agent.
-- Returns the affected rows so callers can broadcast task:cancelled events.
-- Mirrors the shape of CancelAgentTasksByIssue / CancelAgentTasksByIssueAndAgent
-- (also :many + RETURNING + completed_at) so the three sibling cancel paths
-- behave consistently.
UPDATE agent_inbox_event
SET status = 'suppressed', terminal_outcome = 'cancelled',
    completed_at = now(), terminal_at = now(), acked_at = now()
WHERE agent_id = $1 AND status IN ('pending', 'draining', 'failed')
RETURNING *;

-- name: CancelAgentTasksByTriggerComment :many
-- Cancels active tasks whose trigger is the given comment. Called when a
-- comment is deleted so the agent does not run with the now-deleted content
-- already embedded in its prompt. Must run BEFORE the comment row is deleted
-- because the FK ON DELETE SET NULL would otherwise nullify trigger_comment_id
-- and we'd lose the ability to find the affected tasks.
UPDATE agent_inbox_event
SET status = 'suppressed', terminal_outcome = 'cancelled',
    completed_at = now(), terminal_at = now(), acked_at = now()
WHERE trigger_comment_id = $1 AND status IN ('pending', 'draining', 'failed')
RETURNING *;

-- name: CancelAgentTasksByChatSession :many
-- Cancels active tasks belonging to a chat session. Called from
-- DeleteChatSession so the daemon doesn't keep running work whose result
-- has nowhere to land. Must run BEFORE the chat_session row is deleted —
-- the FK ON DELETE SET NULL would otherwise nullify chat_session_id and we
-- could no longer reach those tasks.
UPDATE agent_inbox_event
SET status = 'suppressed', terminal_outcome = 'cancelled',
    completed_at = now(), terminal_at = now(), acked_at = now()
WHERE chat_session_id = $1 AND status IN ('pending', 'draining', 'failed')
RETURNING *;

-- name: GetAgentTask :one
SELECT * FROM agent_inbox_event
WHERE id = $1;

-- name: GetAgentTaskInWorkspace :one
-- Loads a task only when its owning agent lives in the given workspace.
-- agent_id is NOT NULL on every task row (and ON DELETE CASCADE, so the agent
-- always exists), which makes this the universal tenant guard for
-- user-initiated cancellation — independent of which optional source FK
-- (issue / chat_session / autopilot_run) happens to be set. It is what lets
-- run_only autopilot tasks and quick_create tasks (whose issue does not exist
-- yet) be cancelled at all, instead of 404-ing on a missing source FK.
SELECT atq.* FROM agent_inbox_event atq
JOIN agent a ON a.id = atq.agent_id
WHERE atq.id = $1 AND a.workspace_id = $2;

-- name: StartAgentTask :one
-- Transitions a claimed delivery to running and clears any generic wait hint.
UPDATE agent_inbox_event AS atq
SET started_at = COALESCE(started_at, now()), wait_reason = NULL
WHERE atq.id = $1
  AND atq.status = 'draining'
RETURNING atq.*;

-- name: CompleteAgentTask :one
UPDATE agent_inbox_event
SET status = 'acked', completed_at = now(), terminal_at = now(),
    terminal_outcome = 'completed', retryable = false, acked_at = now(),
    result = $2, session_id = $3, work_dir = $4
WHERE id = $1 AND status = 'draining'
RETURNING *;

-- name: GetLastTaskSession :one
-- Returns the session_id and work_dir from the most recent task for a given
-- (agent_id, issue_id) pair, used for session resumption on the auto-retry
-- path. We accept both 'completed' and 'failed' tasks: a failed task may
-- have established a real agent session before crashing (orphaned by a
-- daemon restart, runtime offline, or sweeper timeout), and the daemon pins
-- the resume pointer mid-flight via UpdateAgentTaskSession. Without this,
-- an auto-retry of a mid-run failure would silently start a fresh
-- conversation and lose the in-flight context — exactly what MUL-1128's B
-- branch is meant to fix.
--
-- Manual rerun (TaskService.RerunIssue) does NOT take this path: it sets
-- force_fresh_session=true on the new task, and the daemon claim handler
-- skips this lookup entirely. The user already judged the prior output bad;
-- resuming the same conversation would replay a poisoned state.
--
-- Tasks that ended in a known "poisoned" terminal state are also excluded
-- here so even auto-retry does not inherit the bad session. The daemon
-- classifies these failures (iteration_limit, agent_fallback_message,
-- api_invalid_request, codex_semantic_inactivity) when it detects either an
-- agent fallback marker in the output, an upstream API 400 that means the
-- conversation history itself is unprocessable (oversized image, malformed
-- base64, etc.), or a Codex semantic inactivity timeout whose recorded
-- session may replay the same stuck state.
--
-- The error-text ILIKE clause is defense-in-depth for the api_invalid_request
-- shape: a legacy row tagged 'agent_error' (pre-MUL-1921), a deploy-window
-- row that the old code wrote between migration and rollout, or a future
-- error format that escapes the daemon classifier all still get filtered
-- here as long as the canonical Anthropic 400 marker is present in the
-- error text. Migration 079 backfills the failure_reason column itself,
-- so observability stays accurate; this clause guarantees session resume
-- never picks up a bad session even when failure_reason hasn't caught up.
SELECT session_id, work_dir, runtime_id FROM agent_inbox_event
WHERE agent_id = $1 AND issue_id = $2
  AND (
    (status = 'acked' AND terminal_outcome = 'completed')
    OR (status = 'suppressed' AND COALESCE(failure_reason, '') = 'followup_interrupt')
    OR (
      status = 'acked'
      AND terminal_outcome = 'failed'
      AND COALESCE(failure_reason, '') NOT IN ('iteration_limit', 'agent_fallback_message', 'api_invalid_request', 'codex_semantic_inactivity', 'grok_first_turn_no_progress', 'agent_error.context_overflow')
      AND NOT (COALESCE(error, '') ILIKE '%400%' AND COALESCE(error, '') ILIKE '%invalid_request_error%')
      AND NOT (COALESCE(error, '') ILIKE '%context window%' OR COALESCE(error, '') ILIKE '%context length%' OR COALESCE(error, '') ILIKE '%context_length_exceeded%' OR COALESCE(error, '') ILIKE '%maximum context%' OR COALESCE(error, '') ILIKE '%prompt is too long%' OR (COALESCE(error, '') ILIKE '%token%' AND COALESCE(error, '') ILIKE '%limit%'))
    )
  )
  AND session_id IS NOT NULL
ORDER BY COALESCE(completed_at, started_at, dispatched_at, created_at) DESC
LIMIT 1;

-- name: GetLastTaskStartedAtForIssueAndAgent :one
-- Returns the started_at of the most recent prior task for this (agent, issue)
-- pair, used as the "since" anchor for counting comments that arrived since the
-- agent's last run. Any terminal state counts as "a run happened". Tasks with
-- no started_at (never dispatched / the just-claimed current task) are excluded,
-- so this never returns the current claim's own row. MUST use started_at, never
-- completed_at: a long run would otherwise miss comments posted while it ran.
SELECT started_at FROM agent_inbox_event
WHERE agent_id = $1 AND issue_id = $2 AND started_at IS NOT NULL
ORDER BY started_at DESC
LIMIT 1;

-- name: FailAgentTask :one
-- Marks a task as failed. session_id and work_dir are merged via COALESCE so
-- if the agent already established a real session before failing (e.g. it
-- crashed mid-conversation, was cancelled, or hit a tool error) the resume
-- pointer is preserved on the task row. The next chat task can then fall
-- back to GetLastChatTaskSession and continue the conversation instead of
-- silently starting over.
--
-- failure_reason is a coarse classifier consumed by the auto-retry path;
-- 'agent_error' is the safe default when the daemon doesn't supply one.
UPDATE agent_inbox_event
SET status = 'acked',
    completed_at = now(),
    terminal_at = now(),
    acked_at = now(),
    terminal_outcome = 'failed',
    retryable = false,
    error = $2,
    failure_reason = COALESCE(sqlc.narg('failure_reason'), 'agent_error'),
    session_id = COALESCE(sqlc.narg('session_id'), session_id),
    work_dir = COALESCE(sqlc.narg('work_dir'), work_dir)
WHERE id = $1 AND status = 'draining'
RETURNING *;

-- name: UpdateAgentTaskSession :exec
-- Pins the provider-CLI resume pointer mid-flight so a daemon crash leaves a
-- usable session_id/work_dir on the task row (PinTaskSession / --resume).
-- This TEXT column is NOT agent_session / agent_session_id (Multica inbox
-- wake/drain UUID). No FK between them; task #109. No-op if the task is no
-- longer draining.
UPDATE agent_inbox_event
SET session_id = COALESCE(sqlc.narg('session_id'), session_id),
    work_dir  = COALESCE(sqlc.narg('work_dir'), work_dir)
WHERE id = $1 AND status = 'draining';

-- name: SetTaskParentTaskID :exec
-- Links a mention-delegated child task to its trained parent (D11 delegation
-- edge). CreateAgentTask has no parent_task_id parameter, so this post-insert
-- UPDATE sets it. Used so the delegation edge parent->child can be recorded at
-- the child's close via SegmentIDForAgentRun(parent_task_id).
UPDATE agent_inbox_event
SET parent_task_id = $2
WHERE id = $1;

-- name: MergeTaskArealProxyContext :exec
-- Merges the training RL proxy config into the task's context JSONB via a
-- single read-modify-write. COALESCE handles a NULL/empty context; the `||`
-- operator preserves every existing top-level key and
-- overwrites only the areal_proxy sub-object. Used by the session-open hook.
-- This intentionally does NOT touch agent_inbox_event.session_id (provider CLI
-- --resume TEXT from PinTaskSession; also not the agent_session inbox UUID —
-- task #109), nor the RL session inside areal_proxy.
UPDATE agent_inbox_event
SET context = COALESCE(context, '{}'::jsonb) || jsonb_build_object('areal_proxy', sqlc.arg('areal_proxy')::jsonb)
WHERE id = sqlc.arg('id');

-- name: StripArealProxyFromTaskContext :exec
-- Removes the areal_proxy sub-object from the task's context JSONB. Used by the
-- retry path (D9): CreateRetryTask copies the parent's context verbatim, so the
-- child would inherit the parent's (now-closed) RL session; stripping forces the
-- child to open a fresh session at its own session-open chokepoint. The `-`
-- operator drops only areal_proxy, preserving every other top-level key
-- and the chat session_id/work_dir resume pointers (separate columns).
-- No-op when the key is absent. Propagates errors (the strip is load-bearing for
-- D9, not best-effort).
UPDATE agent_inbox_event
SET context = COALESCE(context, '{}'::jsonb) - 'areal_proxy'
WHERE id = $1;

-- name: RecoverOrphanedTasksForRuntime :many
-- Called by the daemon at startup. Atomically fails any in-flight delivery
-- that the prior incarnation of this runtime owned but did not finalize.
UPDATE agent_inbox_event
SET status = 'acked',
    completed_at = now(),
    terminal_at = now(),
    acked_at = now(),
    terminal_outcome = 'failed',
    error = 'daemon restarted while event was in flight',
    last_error = 'daemon restarted while event was in flight',
    failure_reason = 'runtime_recovery',
    retryable = false,
    wait_reason = NULL
WHERE runtime_id = $1 AND status = 'draining'
RETURNING *;

-- name: FailStaleTasks :many
-- Fails tasks stuck in dispatched/running beyond the given thresholds.
-- Handles cases where the daemon is alive but the task is orphaned
-- (e.g. agent process hung, daemon failed to report completion).
UPDATE agent_inbox_event
SET status = 'acked',
    completed_at = now(),
    terminal_at = now(),
    acked_at = now(),
    terminal_outcome = 'failed',
    error = 'event delivery timed out',
    last_error = 'event delivery timed out',
    failure_reason = 'timeout',
    retryable = false
WHERE status = 'draining'
  AND (
    (started_at IS NULL AND claimed_at < now() - make_interval(secs => @dispatch_timeout_secs::double precision))
    OR (started_at < now() - make_interval(secs => @running_timeout_secs::double precision))
  )
RETURNING *;

-- name: ExpireStaleQueuedTasks :many
-- Fails tasks that have been sitting in 'queued' for longer than the TTL —
-- but only while their runtime is not confirmed 'offline'. This is the
-- cleanup arm of the MUL-1899 "queued backlog" fix: even with the
-- dispatch-time admission gate that refuses to enqueue when the runtime
-- is offline, we still need to drain the historical 87k+ doomed rows and
-- handle edge cases where a runtime goes offline AFTER a task is already
-- queued (the admission check protects new enqueues, not in-flight queue
-- depth).
--
-- Task #50: a task queued behind an offline runtime must not be reaped by
-- this blind clock — "offline" is not "never coming back" (a laptop closed
-- overnight looks identical to a dead machine from here), so time alone is
-- the wrong signal.
--
-- The exclusion is computed directly from heartbeat freshness
-- (last_seen_at vs @stale_threshold_secs — the same signal and threshold
-- SelectStaleOnlineRuntimes uses, passed in as staleThresholdSeconds from
-- runtime_sweeper.go), NOT from the persisted agent_runtime.status column.
-- Reading status here would make this query's correctness depend on
-- sweepStaleRuntimes having already run earlier in the same sweep tick —
-- true today only because of an unenforced call-order coincidence in
-- runRuntimeSweeper. #1664 already established the rule for the read-time
-- display status (agentRuntimeDisplayStatus): derive from heartbeat
-- freshness at read time, don't trust the (sweep-lagged, up to ~180s stale)
-- persisted column. This query follows the same rule for the same reason.
--
-- A resident provider process crashing (recoverable — task #42②) does not
-- affect the daemon's heartbeat, so last_seen_at stays fresh for that case
-- and this TTL backstop still applies unchanged; it only ever fires for a
-- genuinely-stuck task behind a healthy runtime, same as before #50. There
-- is deliberately no separate "offline too long, give up" timeout here —
-- that would just be the same blind-clock mistake at a bigger number. A
-- queued task's terminal states are event-driven only (deleted agent, agent
-- moved to a different runtime, explicit cancel), never elapsed wall-clock
-- time on a merely-offline runtime.
--
-- Concurrency safety: the daemon's claim path may race with this sweeper to
-- transition the same row out of 'queued'. We protect against that two
-- ways:
--   1. The CTE selects victims with FOR UPDATE SKIP LOCKED so a row that is
--      currently being claimed (or otherwise locked) is skipped — no lock
--      contention with the dispatch path, and we won't queue up behind it.
--   2. The outer UPDATE re-checks status='queued' AND the TTL predicate at
--      apply time. If a daemon claimed the row between selection and update
--      (e.g. lock released after the claim transaction commits), the row is
--      already 'dispatched'/'running' and the WHERE clause filters it out
--      so we cannot clobber an in-flight task.
-- Capped via LIMIT inside the CTE so a single sweep tick cannot monopolise
-- the DB when the backlog is large — the sweeper drains the rest on
-- subsequent ticks.
WITH victims AS (
    SELECT t.id
    FROM agent_inbox_event t
    LEFT JOIN agent_runtime r ON r.id = t.runtime_id
    WHERE t.status IN ('pending', 'failed')
      AND t.created_at < now() - make_interval(secs => @ttl_secs::double precision)
      AND (
        r.id IS NULL
        OR r.last_seen_at IS NULL
        OR r.last_seen_at >= now() - make_interval(secs => @stale_threshold_secs::double precision)
      )
    ORDER BY t.created_at ASC
    LIMIT @max_per_tick::int
    FOR UPDATE OF t SKIP LOCKED
)
UPDATE agent_inbox_event t
SET status = 'acked',
    completed_at = now(),
    terminal_at = now(),
    acked_at = now(),
    terminal_outcome = 'failed',
    error = 'task expired in queue',
    failure_reason = 'queued_expired'
FROM victims v
WHERE t.id = v.id
  AND t.status IN ('pending', 'failed')
  AND t.created_at < now() - make_interval(secs => @ttl_secs::double precision)
  AND NOT EXISTS (
    SELECT 1 FROM agent_runtime r
    WHERE r.id = t.runtime_id
      AND r.last_seen_at IS NOT NULL
      AND r.last_seen_at < now() - make_interval(secs => @stale_threshold_secs::double precision)
  )
RETURNING t.*;

-- name: ExpireQueuedTasksOnOfflineRuntimes :many
-- Fails 'queued' tasks whose runtime is still 'offline' after the
-- offline-runtime TTL. This is the env-dispatch per-agent sandbox liveness
-- backstop (Phase 2b): env-dispatch pre-creates R' as offline, routes a queued
-- task to it, and relies on the in-sandbox daemon registering (-> R' online)
-- and claiming the task. If the sandbox/daemon never comes up, the task would
-- otherwise sit in 'queued' until the slow 2h queued-TTL sweep. This drains it
-- in ~5 min so the rollout fails promptly. FailTasksForOfflineRuntimes does NOT
-- cover this: it only touches in-flight tasks, so
-- 'queued' rows on offline runtimes are otherwise only caught by the 2h sweep.
--
-- Same concurrency contract as ExpireStaleQueuedTasks: FOR UPDATE SKIP LOCKED
-- in the victim CTE + a status='queued' AND runtime-still-offline re-check in
-- the outer UPDATE. The re-check matters here because the daemon may bring R'
-- online and claim the task between selection and apply - in that case the row
-- is already 'dispatched' (filtered by status) or R' is now online (filtered by
-- the EXISTS), so we cannot clobber an in-flight or now-routable task.
-- failure_reason='runtime_offline' matches FailTasksForOfflineRuntimes so the
-- retry/metrics path in HandleFailedTasks treats it identically (retryable,
-- bounded by max_attempts).
WITH victims AS (
    SELECT t.id
    FROM agent_inbox_event t
    JOIN agent_runtime r ON r.id = t.runtime_id
    WHERE t.status IN ('pending', 'failed')
      AND r.status = 'offline'
      AND NULLIF(t.context->'ephemeral_sandbox'->>'sandbox_instance_id', '') IS NOT NULL
      AND t.created_at < now() - make_interval(secs => @ttl_secs::double precision)
    ORDER BY t.created_at ASC
    LIMIT @max_per_tick::int
    FOR UPDATE SKIP LOCKED
)
UPDATE agent_inbox_event t
SET status = 'acked',
    completed_at = now(),
    terminal_at = now(),
    acked_at = now(),
    terminal_outcome = 'failed',
    error = 'runtime offline: sandbox daemon did not register in time',
    failure_reason = 'runtime_offline'
FROM victims v
WHERE t.id = v.id
  AND t.status IN ('pending', 'failed')
  AND NULLIF(t.context->'ephemeral_sandbox'->>'sandbox_instance_id', '') IS NOT NULL
  AND t.created_at < now() - make_interval(secs => @ttl_secs::double precision)
  AND EXISTS (SELECT 1 FROM agent_runtime r WHERE r.id = t.runtime_id AND r.status = 'offline')
RETURNING t.*;

-- name: CancelAgentTask :one
UPDATE agent_inbox_event
SET status = 'suppressed', terminal_outcome = 'cancelled',
    completed_at = now(), terminal_at = now(), acked_at = now()
WHERE id = $1 AND status IN ('pending', 'draining', 'failed')
RETURNING *;

-- name: CountRunningTasks :one
SELECT count(*) FROM agent_inbox_event
WHERE agent_id = $1
  AND chat_session_id IS NULL
  AND status = 'draining';

-- name: CountRunningChatTasks :one
SELECT count(*) FROM agent_inbox_event
WHERE agent_id = $1
  AND chat_session_id IS NOT NULL
  AND status = 'draining';

-- name: HasActiveTaskForIssue :one
-- Returns true if there is any active task for the issue.
SELECT count(*) > 0 AS has_active FROM agent_inbox_event
WHERE issue_id = $1 AND status IN ('pending', 'draining', 'failed');

-- name: HasOtherActiveTaskForRuntime :one
-- Returns true if any active task
-- OTHER than the given one is still bound to this runtime. Used by the Phase 5
-- ephemeral-sandbox cleanup guard: a retry child inherits the parent's runtime_id
-- (CreateRetryTask copies runtime_id), so the sandbox + runtime must not be
-- torn down while a sibling/child task is still active on R'.
SELECT count(*) > 0 AS has_active FROM agent_inbox_event
WHERE runtime_id = sqlc.arg('runtime_id')
  AND id <> sqlc.arg('exclude_task')
  AND status IN ('pending', 'draining', 'failed');

-- name: HasPendingTaskForIssue :one
-- Returns true if there is a queued or dispatched (but not yet running) task for the issue.
-- Used by the coalescing queue: allow enqueue when a task is running (so
-- the agent picks up new comments on the next cycle) but skip if a pending
-- task already exists (natural dedup).
SELECT count(*) > 0 AS has_pending FROM agent_inbox_event
WHERE issue_id = $1 AND status IN ('pending', 'failed');

-- name: HasPendingTaskForIssueAndAgent :one
-- Returns true if a specific agent already has a queued or dispatched task
-- for the given issue. Used by @mention trigger dedup.
SELECT count(*) > 0 AS has_pending FROM agent_inbox_event
WHERE issue_id = $1 AND agent_id = $2 AND status IN ('pending', 'failed');

-- name: ListPendingTasksByRuntime :many
SELECT * FROM agent_inbox_event
WHERE runtime_id = $1 AND status IN ('pending', 'draining', 'failed')
ORDER BY priority DESC, created_at ASC;

-- name: ListActiveTasksByIssue :many
-- Backs the issue-detail "agent live" banner. Includes 'queued' so the
-- banner shows up the moment a task is enqueued — not only after a runtime
-- claims it. The queued window can be long when the runtime is offline or
-- busy on a prior task, and a silent UI during that window looks like the
-- platform never received the trigger.
SELECT * FROM agent_inbox_event
WHERE issue_id = $1 AND status IN ('pending', 'draining', 'failed')
ORDER BY created_at DESC;

-- name: GetWorkspaceAgentRunCounts :many
-- Total task runs per agent over the trailing 30 days, used by the Agents
-- list RUNS column. 30-day window keeps the count meaningful (a long-dormant
-- agent shouldn't show "5,420 runs from 2 years ago") and keeps the scan
-- bounded as the workspace ages.
SELECT
    atq.agent_id,
    COUNT(*)::int AS run_count
FROM agent_inbox_event atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = $1
  AND atq.created_at > now() - INTERVAL '30 days'
  AND COALESCE(atq.context->>'type', '') <> 'agent_radar'
GROUP BY atq.agent_id;

-- name: GetWorkspaceAgentActivity30d :many
-- Returns per-agent daily activity buckets for the last 30 days. Single
-- workspace-wide read backs both surfaces:
--   - Agents list ACTIVITY column — uses only the trailing 7 buckets
--   - Agent detail "Last 30 days" panel — uses the full 30
-- 30 days contains 7 days, so one fetch + a client-side .slice(-7) wins
-- over fetching twice. Days with no completion produce no row; the
-- front-end zero-fills.
--
-- Anchored on completed_at (not created_at) because the sparkline answers
-- "what did this agent produce?" not "what was queued at it?". A task that's
-- still in flight has no completed_at and contributes nothing here — that's
-- correct: in-flight tasks are surfaced via the live presence indicator,
-- not the historical trend.
SELECT
    atq.agent_id,
    DATE_TRUNC('day', atq.completed_at)::timestamptz AS bucket,
    COUNT(*)::int AS task_count,
    COUNT(*) FILTER (WHERE atq.status = 'acked' AND atq.terminal_outcome = 'failed')::int AS failed_count
FROM agent_inbox_event atq
WHERE atq.workspace_id = $1
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at > now() - INTERVAL '30 days'
  AND COALESCE(atq.context->>'type', '') <> 'agent_radar'
GROUP BY atq.agent_id, bucket
ORDER BY atq.agent_id, bucket;

-- name: ListWorkspaceAgentTaskSnapshot :many
-- Returns the tasks needed to derive each agent's current presence:
--   - All active tasks (queued / dispatched / running) — for working signal + counts
--   - Each agent's most recent OUTCOME task (completed / failed) — for sticky
--     failed signal
-- The front-end picks "active wins, else latest outcome" — see derive-presence.ts.
--
-- Cancelled tasks are excluded from the outcome half on purpose: cancel is a
-- procedural signal ("attempt aborted"), not an outcome. It tells us nothing
-- about whether the agent works, so it must NOT be allowed to mask a prior
-- failure. Concretely: if an agent fails and then the user cancels the queued
-- retry (or the parent issue closes and cascades cancels), the failed signal
-- has to stay red. Only a real success (completed) or a fresh attempt (active)
-- clears it.
--
-- No UI windows in SQL: stickiness is decided by "is the latest outcome a
-- failure?", not a 2-minute clock. Active events filter directly by their
-- workspace_id. Latest outcomes enumerate non-archived workspace agents and do
-- one ordered index probe per agent (idx_agent_inbox_event_workspace_agent_outcome),
-- so history growth does not turn this hot snapshot into a full-history scan.
--
-- Hot-path payload (LRM-1261): omit heavy blobs (context/result/execution_config/
-- error) from the SELECT list — presence/FE snapshot consumers only need status,
-- ids, timestamps, and trigger summary. WHERE still uses context->>'type' for
-- quick_create visibility. Keep LATERAL (not DISTINCT ON) so each agent is a
-- LIMIT-1 index probe instead of scanning every historical outcome row.
SELECT
  atq.id, atq.workspace_id, atq.agent_session_id, atq.conversation_id, atq.channel_id,
  atq.chat_session_id, atq.agent_id, atq.source_message_id, atq.reason, atq.requires_wake,
  atq.status, atq.priority, atq.seq_from, atq.seq_to, atq.attempt, atq.last_error,
  atq.claimed_at, atq.acked_at, atq.created_at, atq.updated_at, atq.terminal_outcome,
  atq.terminal_delivery_id, atq.retryable, atq.terminal_at, atq.runtime_id,
  NULL::jsonb AS execution_config,
  atq.delivery_mode, atq.response_mode, atq.channel_onboarding_id, atq.issue_id,
  atq.source_chat_message_id,
  NULL::jsonb AS context,
  atq.dispatched_at, atq.started_at, atq.completed_at,
  NULL::jsonb AS result,
  NULL::text AS error,
  atq.session_id, atq.work_dir, atq.trigger_comment_id, atq.autopilot_run_id,
  atq.max_attempts, atq.parent_task_id, atq.failure_reason, atq.trigger_summary,
  atq.force_fresh_session, atq.is_leader_task, atq.wait_reason, atq.initiator_user_id
FROM agent_inbox_event atq
WHERE atq.workspace_id = $1
  AND atq.status IN ('pending', 'draining', 'failed')
  AND (
    atq.issue_id IS NOT NULL
    OR atq.chat_session_id IS NOT NULL
    OR atq.channel_id IS NOT NULL
    OR atq.autopilot_run_id IS NOT NULL
    OR (
      atq.context->>'type' = 'quick_create'
      AND atq.context->>'workspace_id' = $1::text
    )
  )

UNION ALL

SELECT
  latest.id, latest.workspace_id, latest.agent_session_id, latest.conversation_id, latest.channel_id,
  latest.chat_session_id, latest.agent_id, latest.source_message_id, latest.reason, latest.requires_wake,
  latest.status, latest.priority, latest.seq_from, latest.seq_to, latest.attempt, latest.last_error,
  latest.claimed_at, latest.acked_at, latest.created_at, latest.updated_at, latest.terminal_outcome,
  latest.terminal_delivery_id, latest.retryable, latest.terminal_at, latest.runtime_id,
  latest.execution_config, latest.delivery_mode, latest.response_mode, latest.channel_onboarding_id,
  latest.issue_id, latest.source_chat_message_id, latest.context, latest.dispatched_at,
  latest.started_at, latest.completed_at, latest.result, latest.error, latest.session_id,
  latest.work_dir, latest.trigger_comment_id, latest.autopilot_run_id, latest.max_attempts,
  latest.parent_task_id, latest.failure_reason, latest.trigger_summary, latest.force_fresh_session,
  latest.is_leader_task, latest.wait_reason, latest.initiator_user_id
FROM agent a
JOIN LATERAL (
  SELECT
    atq.id, atq.workspace_id, atq.agent_session_id, atq.conversation_id, atq.channel_id,
    atq.chat_session_id, atq.agent_id, atq.source_message_id, atq.reason, atq.requires_wake,
    atq.status, atq.priority, atq.seq_from, atq.seq_to, atq.attempt, atq.last_error,
    atq.claimed_at, atq.acked_at, atq.created_at, atq.updated_at, atq.terminal_outcome,
    atq.terminal_delivery_id, atq.retryable, atq.terminal_at, atq.runtime_id,
    NULL::jsonb AS execution_config,
    atq.delivery_mode, atq.response_mode, atq.channel_onboarding_id, atq.issue_id,
    atq.source_chat_message_id,
    NULL::jsonb AS context,
    atq.dispatched_at, atq.started_at, atq.completed_at,
    NULL::jsonb AS result,
    NULL::text AS error,
    atq.session_id, atq.work_dir, atq.trigger_comment_id, atq.autopilot_run_id,
    atq.max_attempts, atq.parent_task_id, atq.failure_reason, atq.trigger_summary,
    atq.force_fresh_session, atq.is_leader_task, atq.wait_reason, atq.initiator_user_id
  FROM agent_inbox_event atq
  WHERE atq.workspace_id = $1
    AND atq.agent_id = a.id
    AND atq.status = 'acked'
    AND atq.terminal_outcome IN ('completed', 'failed')
    AND (
      atq.issue_id IS NOT NULL
      OR atq.chat_session_id IS NOT NULL
      OR atq.channel_id IS NOT NULL
      OR atq.autopilot_run_id IS NOT NULL
      OR (
        atq.context->>'type' = 'quick_create'
        AND atq.context->>'workspace_id' = $1::text
      )
    )
  ORDER BY atq.agent_id, atq.completed_at DESC NULLS LAST, atq.id DESC
  LIMIT 1
) latest ON TRUE
WHERE a.workspace_id = $1
  AND a.archived_at IS NULL;

-- name: ListTasksByIssue :many
SELECT * FROM agent_inbox_event
WHERE issue_id = $1
ORDER BY created_at DESC;

-- name: UpdateAgentStatus :one
UPDATE agent SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RefreshAgentStatusFromTasks :one
-- Provider-quota lock (tasks #64/#77) wins over workload: a blocked agent
-- must not flip back to idle/working just because the draining task ended.
-- Lock = a real detail (not "{}" / other JSON leftovers) AND (until unknown
-- OR until > now()).
UPDATE agent AS a
SET status = CASE
    WHEN btrim(COALESCE(a.provider_block_detail, '')) <> ''
         AND lower(btrim(a.provider_block_detail)) NOT IN ('{}', '[]', 'null', 'undefined', '""')
         AND (a.provider_blocked_until IS NULL OR a.provider_blocked_until > now())
      THEN 'blocked'
    WHEN EXISTS (
        SELECT 1 FROM agent_inbox_event q
        WHERE q.agent_id = a.id AND q.status = 'draining'
    ) THEN 'working'
    ELSE 'idle'
END,
    updated_at = now()
WHERE a.id = $1
RETURNING *;

-- name: ResetInFlightTaskForResume :one
-- Re-activates a specific in-flight task for resume-from-checkpoint by
-- returning it to `queued` so the resumed runtime's claim loop re-claims it.
-- Only non-terminal (running/dispatched) tasks bound to the given runtime are
-- eligible; a terminal task or a runtime mismatch returns no rows (caller
-- treats that as a stale/unresumable trigger). Preserves context/runtime_id/
-- issue_id/chat_session_id - this is the SAME task row, not a new one.
UPDATE agent_inbox_event
SET status = 'pending', started_at = NULL, dispatched_at = NULL,
    claimed_at = NULL, updated_at = now()
WHERE id = @task_id
  AND runtime_id = @runtime_id
  AND status = 'draining'
RETURNING *;

-- name: ListInFlightTasksForProject :many
-- Resolves a project's in-flight (running/dispatched) agent tasks for
-- resume-trigger capture at checkpoint-create time. Joins via issue or
-- chat_session to the project; project_id is workspace-scoped by nature.
SELECT atq.* FROM agent_inbox_event atq
LEFT JOIN issue i ON atq.issue_id = i.id
LEFT JOIN chat_session cs ON atq.chat_session_id = cs.id
WHERE (i.project_id = @project_id OR cs.project_id = @project_id)
  AND atq.status = 'draining'
ORDER BY atq.created_at ASC;

-- name: MarkAgentCrashed :exec
-- Records that this agent's idle resident provider process was found dead
-- (daemon ResidentRuntimeCrashEvent / task #42②). Per-agent on purpose: many
-- agents can share one runtime/daemon, and only the crashed provider slot
-- should show "crashed". Cleared explicitly on successful recreate / manual
-- lifecycle restart — not by TTL (a crash with no next dispatch is exactly
-- the long-lived case this signal exists for).
UPDATE agent
SET crashed_since = now(), updated_at = now()
WHERE id = $1;

-- name: ClearAgentCrashed :exec
-- Clears a prior MarkAgentCrashed once the daemon has a live resident
-- provider again (successful recreate after eviction) or a human-driven
-- lifecycle restart succeeded. Leaving this out would stick "crashed" forever
-- after recovery — the same class of stale-fact bug as uncleared offline_reason.
UPDATE agent
SET crashed_since = NULL, updated_at = now()
WHERE id = $1;

-- name: ListAgentCrashedSinceByIDs :many
-- Narrow read used by attachAgentRuntimeNames so GET /agents can project
-- "crashed" without regenerating every SELECT * FROM agent query (make sqlc
-- is currently broken repo-wide — see task #83). Keep this in sync with the
-- column MarkAgentCrashed/ClearAgentCrashed touch.
SELECT id, crashed_since
FROM agent
WHERE id = ANY($1::uuid[]) AND crashed_since IS NOT NULL;

-- name: MarkAgentProviderBlocked :exec
-- Pins agent display as provider-quota blocked (tasks #64/#77). Extends or
-- replaces an existing lock. Does not clear on heartbeat. until may be NULL
-- (= locked, unknown end). Claim/drain gating is a separate card.
UPDATE agent
SET provider_blocked_until = $2,
    provider_block_detail = $3,
    status = 'blocked',
    updated_at = now()
WHERE id = $1;

-- name: ClearAgentProviderBlocked :exec
-- Explicit clear (e.g. admin override). Normal unlock is read-time when a
-- known until elapses — RefreshAgentStatusFromTasks then leaves blocked.
UPDATE agent
SET provider_blocked_until = NULL,
    provider_block_detail = '',
    updated_at = now()
WHERE id = $1;

-- name: ListAgentProviderBlockByIDs :many
-- Narrow read for attachAgentRuntimeNames (same make-sqlc constraint as
-- ListAgentCrashedSinceByIDs). Active locks only (unknown-until stays active).
SELECT id, provider_blocked_until, provider_block_detail
FROM agent
WHERE id = ANY($1::uuid[])
  AND btrim(COALESCE(provider_block_detail, '')) <> ''
  AND lower(btrim(provider_block_detail)) NOT IN ('{}', '[]', 'null', 'undefined', '""')
  AND (provider_blocked_until IS NULL OR provider_blocked_until > now());
