-- name: CreateChatSession :one
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, runtime_id)
VALUES ($1, $2, $3, $4, (SELECT runtime_id FROM agent WHERE id = $2))
RETURNING *;

-- name: GetChatSession :one
SELECT * FROM chat_session
WHERE id = $1;

-- name: GetChatSessionInWorkspace :one
SELECT * FROM chat_session
WHERE id = $1 AND workspace_id = $2;

-- name: ListChatSessionsByCreator :many
-- Returns active sessions with a boolean unread flag. Unread is strictly
-- per-session: either the user has uncleared assistant replies in this
-- session or they don't. Counting messages would be misleading.
SELECT cs.*,
       (cs.unread_since IS NOT NULL)::bool AS has_unread
FROM chat_session cs
WHERE cs.workspace_id = $1 AND cs.creator_id = $2 AND cs.status = 'active'
ORDER BY cs.updated_at DESC;

-- name: ListAllChatSessionsByCreator :many
SELECT cs.*,
       (cs.unread_since IS NOT NULL)::bool AS has_unread
FROM chat_session cs
WHERE cs.workspace_id = $1 AND cs.creator_id = $2
ORDER BY cs.updated_at DESC;

-- name: UpdateChatSessionTitle :one
UPDATE chat_session SET title = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateChatSessionStatus :one
-- Soft-archive / restore. Only 'active' and 'archived' are valid.
UPDATE chat_session SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateChatSessionSession :exec
-- Updates the resume pointer for a chat session. Empty/NULL inputs are
-- ignored via COALESCE so a task that completes without a session_id (e.g.
-- the agent crashed before establishing one) cannot wipe out a previously
-- recorded resume pointer. This makes the chat memory robust against
-- intermittent agent failures.
UPDATE chat_session
SET session_id = COALESCE(sqlc.narg('session_id'), session_id),
    work_dir = COALESCE(sqlc.narg('work_dir'), work_dir),
    runtime_id = COALESCE(sqlc.narg('runtime_id'), runtime_id),
    updated_at = now()
WHERE id = sqlc.arg('id');

-- name: ClearChatSessionResumeIfMatch :exec
-- Clears a poisoned chat resume pointer only when it still identifies the
-- failed task's session. The match guard preserves a newer pointer written by
-- another completed task while this failure was being finalized.
UPDATE chat_session
SET session_id = NULL,
    work_dir = NULL,
    runtime_id = NULL,
    updated_at = now()
WHERE id = sqlc.arg('id')
  AND session_id = sqlc.arg('session_id');

-- name: LockChatSessionForDelete :one
-- Acquires an exclusive (FOR UPDATE) row lock on chat_session(id). Used by
-- the delete path so that a concurrent SendChatMessage cannot enqueue a new
-- agent_inbox_event row referencing this session between our cancel and
-- delete steps. The FK from agent_inbox_event.chat_session_id takes a
-- KEY SHARE lock on the parent row during INSERT validation, which
-- conflicts with FOR UPDATE — concurrent inserts block here and then fail
-- their FK check after we commit the delete.
SELECT id FROM chat_session
WHERE id = $1
FOR UPDATE;

-- name: DeleteChatSession :exec
-- Hard delete. chat_message rows cascade via FK ON DELETE CASCADE; the
-- chat_session_id on agent_inbox_event is set NULL by FK so completed/failed
-- task history survives the session being removed. Callers MUST run inside
-- the same transaction that holds LockChatSessionForDelete and that has
-- already cancelled any in-flight tasks (see CancelAgentTasksByChatSession)
-- so the daemon does not keep running work whose result has nowhere to
-- land. workspace_id in the WHERE clause is a SQL-layer tenant guard; see
-- DeleteIssue.
DELETE FROM chat_session WHERE id = $1 AND workspace_id = $2;

-- name: TouchChatSession :exec
UPDATE chat_session SET updated_at = now()
WHERE id = $1;

-- name: CreateChatMessage :one
INSERT INTO chat_message (chat_session_id, role, content, parts, task_id, failure_reason, elapsed_ms)
VALUES ($1, $2, $3, COALESCE(sqlc.arg(parts)::jsonb, '[]'::jsonb), sqlc.narg(task_id), sqlc.narg(failure_reason), sqlc.narg(elapsed_ms))
RETURNING *;

-- name: LinkChatMessageToTask :exec
UPDATE chat_message
SET task_id = $2
WHERE id = $1 AND role = 'user';

-- name: DeleteUserChatMessageByTask :one
DELETE FROM chat_message
WHERE task_id = $1 AND role = 'user'
RETURNING *;

-- name: ListChatMessages :many
SELECT * FROM chat_message
WHERE chat_session_id = $1
ORDER BY created_at ASC;

-- name: ListChatMessagesPage :many
SELECT * FROM chat_message
WHERE chat_session_id = $1
  AND (
    sqlc.narg('before_created_at')::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg('before_created_at')::timestamptz, sqlc.narg('before_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: GetChatMessage :one
SELECT * FROM chat_message
WHERE id = $1;

-- name: CreateChatTask :one
INSERT INTO agent_inbox_event (
  workspace_id, agent_session_id, agent_id, runtime_id, execution_config,
  issue_id, reason, requires_wake, status, priority, chat_session_id,
  initiator_user_id, force_fresh_session, context
)
SELECT
  a.workspace_id, ensure_agent_wake_session(a.id), a.id, sqlc.arg(runtime_id),
  -- reason=chat_session: standalone FAB/bubble only. Channel chat no longer
  -- dual-writes inbox wakes; do not reuse the residual channel reason 'dm'.
  sqlc.narg('context'), NULL, 'chat_session', true, 'pending',
  sqlc.arg(priority), sqlc.arg(chat_session_id), sqlc.arg(initiator_user_id),
  COALESCE(sqlc.narg('force_fresh_session')::boolean, FALSE),
  sqlc.narg('context')
FROM agent a
WHERE a.id = sqlc.arg(agent_id)
RETURNING *;

-- name: GetLastChatTaskSession :one
-- Returns the most recent task in this chat session that managed to record a
-- session_id. Includes both completed and failed tasks: even a failed task
-- may have established a real agent session before failing, and we'd rather
-- resume there than start over and lose conversation memory. Used as a
-- fallback when chat_session.session_id is NULL. Resume-unsafe failures are
-- excluded because replaying those sessions deterministically reproduces the
-- same terminal state.
SELECT session_id, work_dir, runtime_id FROM agent_inbox_event
WHERE chat_session_id = $1
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

-- name: GetPendingChatTask :one
-- Returns the most recent in-flight task for a chat session, if any.
-- Used by the frontend to recover pending state after refresh / reopen.
-- created_at is the anchor for the chat StatusPill timer (it computes
-- elapsed = now - task.created_at), so the pill survives refresh / reopen
-- without "resetting to 0s".
SELECT id,
       CASE
         WHEN status IN ('pending', 'failed') THEN 'queued'
         WHEN status = 'draining' THEN 'running'
         ELSE status
       END AS status,
       created_at,
       id AS inbox_event_id
FROM agent_inbox_event
WHERE chat_session_id = $1
  AND status IN ('pending', 'draining', 'failed')
ORDER BY created_at DESC
LIMIT 1;

-- name: ListPendingChatTasksByCreator :many
-- Aggregate view of all in-flight chat tasks owned by a given creator in a
-- workspace. Drives the FAB's "running" indicator when the chat window is
-- closed and no single session's query is active.
SELECT atq.id AS task_id, atq.status, atq.chat_session_id
FROM agent_inbox_event atq
JOIN chat_session cs ON cs.id = atq.chat_session_id
WHERE cs.workspace_id = $1
  AND cs.creator_id = $2
  AND atq.status IN ('pending', 'draining', 'failed')
ORDER BY atq.created_at DESC;

-- name: MarkChatSessionRead :exec
-- Clears unread_since, dropping the session's unread count to 0.
UPDATE chat_session SET unread_since = NULL
WHERE id = $1;

-- name: SetUnreadSinceIfNull :exec
-- Atomically stamps the first unread assistant message's arrival time.
-- No-op if the session is already in "has unread" state — keeps the earliest
-- unread boundary stable across multiple incoming replies.
UPDATE chat_session SET unread_since = now()
WHERE id = $1 AND unread_since IS NULL;

-- name: GetMostRecentUserChatMessage :one
-- Returns the most recent role='user' message in a session. Used by the
-- Lark `/issue` command parser: when the user types `/issue` with no
-- title, the spec falls back to "use the previous user message as the
-- title". Bot replies (role='assistant') are excluded — only human
-- input qualifies as a fallback title source.
SELECT * FROM chat_message
WHERE chat_session_id = $1 AND role = 'user'
ORDER BY created_at DESC
LIMIT 1;

-- name: CreateChatSessionForProject :one
INSERT INTO chat_session (workspace_id, project_id, agent_id, creator_id, title, runtime_id)
VALUES ($1, $2, $3, $4, $5, (SELECT runtime_id FROM agent WHERE id = $3))
RETURNING *;

-- name: ListChatSessionsByProject :many
SELECT * FROM chat_session
WHERE project_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;
