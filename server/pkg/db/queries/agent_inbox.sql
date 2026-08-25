-- name: UpsertAgentSession :one
-- agent_session (this table) is Multica's inbox wake/drain row: UUID PK,
-- status IN ('active','paused','closed'), joined as agent_inbox_event.agent_session_id.
-- It is NOT agent_inbox_event.session_id (TEXT) — that column is the provider CLI
-- resume token written by PinTaskSession / --resume. No FK links the two; task #109.
INSERT INTO agent_session (
  workspace_id,
  agent_id,
  runtime_id,
  conversation_id,
  channel_id,
  chat_session_id,
  scope,
  status
)
SELECT
  $1,
  $2,
  a.runtime_id,
  $3,
  sqlc.narg('channel_id'),
  sqlc.narg('chat_session_id'),
  $4,
  'active'
FROM agent a
WHERE a.id = $2
ON CONFLICT (workspace_id, agent_id, conversation_id)
DO UPDATE SET
  runtime_id = EXCLUDED.runtime_id,
  channel_id = COALESCE(EXCLUDED.channel_id, agent_session.channel_id),
  chat_session_id = COALESCE(EXCLUDED.chat_session_id, agent_session.chat_session_id),
  scope = EXCLUDED.scope,
  status = 'active',
  updated_at = now()
RETURNING *;

-- name: GetAgentSession :one
SELECT * FROM agent_session
WHERE id = $1;

-- name: CreateAgentInboxEvent :one
INSERT INTO agent_inbox_event (
  workspace_id,
  agent_session_id,
  conversation_id,
  channel_id,
  chat_session_id,
  agent_id,
  runtime_id,
  execution_config,
  source_message_id,
  trigger_summary,
  reason,
  requires_wake,
  status,
  priority,
  seq_from,
  seq_to
)
VALUES (
  $1,
  $2,
  $3,
  sqlc.narg('channel_id'),
  sqlc.narg('chat_session_id'),
  $4,
  (SELECT runtime_id FROM agent WHERE id = $4),
  (SELECT jsonb_build_object(
      'model', COALESCE(model, ''),
      'thinking_level', COALESCE(thinking_level, ''),
      'snapshotted', true
    ) FROM agent WHERE id = $4),
  sqlc.narg('source_message_id'),
  (SELECT LEFT(content, 200) FROM channel_message WHERE id = sqlc.narg('source_message_id')),
  $5,
  $6,
  'pending',
  $7,
  $8,
  $9
)
RETURNING *;

-- name: UpsertAmbientAgentInboxEvent :one
INSERT INTO agent_inbox_event (
  workspace_id,
  agent_session_id,
  conversation_id,
  channel_id,
  agent_id,
  runtime_id,
  execution_config,
  source_message_id,
  reason,
  delivery_mode,
  response_mode,
  requires_wake,
  status,
  priority,
  seq_from,
  seq_to
)
VALUES (
  $1,
  $2,
  $3,
  $4,
  $5,
  (SELECT runtime_id FROM agent WHERE id = $5),
  (SELECT jsonb_build_object('execution_config', jsonb_build_object(
      'model', COALESCE(model, ''),
      'thinking_level', COALESCE(thinking_level, ''),
      'execution_profile', 'full',
      'snapshotted', true
    )) FROM agent WHERE id = $5),
  sqlc.narg('source_message_id'),
  'ambient',
  'observe',
  'no_public_output',
  false,
  'pending',
  0,
  $6,
  $7
)
ON CONFLICT (conversation_id, agent_id)
  WHERE reason = 'ambient' AND delivery_mode = 'observe' AND status IN ('pending', 'failed') AND conversation_id IS NOT NULL
DO UPDATE SET
  agent_session_id = EXCLUDED.agent_session_id,
  channel_id = EXCLUDED.channel_id,
  source_message_id = COALESCE(EXCLUDED.source_message_id, agent_inbox_event.source_message_id),
  status = 'pending',
  seq_from = LEAST(agent_inbox_event.seq_from, EXCLUDED.seq_from),
  seq_to = GREATEST(agent_inbox_event.seq_to, EXCLUDED.seq_to),
  updated_at = now()
RETURNING *;

-- name: GetAgentInboxEvent :one
SELECT * FROM agent_inbox_event
WHERE id = $1;

-- name: GetAgentEventDelivery :one
SELECT * FROM agent_event_delivery
WHERE id = $1;

-- name: CountPendingAgentInboxEventsForRuntime :one
-- Joins agent_session (inbox wake/drain status='active'), NOT the TEXT
-- agent_inbox_event.session_id resume token from PinTaskSession (task #109).
SELECT count(*)
FROM agent_inbox_event e
JOIN agent_session s ON s.id = e.agent_session_id
WHERE COALESCE(e.runtime_id, s.runtime_id) = $1
  AND s.status = 'active'
  AND e.status IN ('pending', 'failed');

-- name: ReclaimExpiredAgentInboxDeliveriesForRuntime :exec
WITH runtime_event AS MATERIALIZED (
  SELECT e.id, e.agent_session_id
  FROM agent_inbox_event e
  JOIN agent_session s ON s.id = e.agent_session_id
  WHERE e.status = 'draining'
    AND s.status = 'active'
    AND (
      e.runtime_id = $1
      OR (e.runtime_id IS NULL AND s.runtime_id = $1)
    )
),
expired_delivery AS (
  UPDATE agent_event_delivery d
  SET status = 'expired',
      last_error = 'delivery lease expired',
      updated_at = now()
  FROM runtime_event e
  WHERE d.agent_session_id = e.agent_session_id
    AND d.inbox_event_id = e.id
    AND d.status IN ('leased', 'processing')
    AND d.lease_expires_at <= now()
  RETURNING d.inbox_event_id
)
UPDATE agent_inbox_event e
SET status = 'pending',
    last_error = 'delivery lease expired',
    updated_at = now()
WHERE e.id IN (SELECT inbox_event_id FROM expired_delivery)
  AND e.status = 'draining'
  AND (
    e.issue_run_kind IS NULL
    OR EXISTS (
      SELECT 1
      FROM active_issue_execution claim
      JOIN issue issue_row
        ON issue_row.workspace_id = claim.workspace_id
       AND issue_row.id = claim.issue_id
       AND issue_row.execution_revision = claim.issue_execution_revision
       AND issue_row.status IN ('todo', 'in_progress')
       AND issue_row.assignee_type = 'agent'
       AND issue_row.assignee_id = claim.agent_id
      WHERE claim.workspace_id = e.workspace_id
        AND claim.issue_id = e.issue_id
        AND claim.run_id = e.id
        AND claim.agent_id = e.agent_id
        AND claim.issue_execution_revision = e.issue_execution_revision
        AND claim.attempt_number = e.issue_execution_attempt_number
        AND claim.status = 'active'
    )
  )
  AND NOT EXISTS (
    SELECT 1
    FROM agent_event_delivery d
    WHERE d.inbox_event_id = e.id
      AND d.status IN ('leased', 'processing')
      AND d.lease_expires_at > now()
  );

-- name: ExpireDeliveriesForRuntimeRecovery :exec
-- Called by RecoverOrphanedTasks (task #107) immediately alongside
-- RecoverOrphanedTasksForRuntime, scoped to the same dead runtime_id.
--
-- RecoverOrphanedTasksForRuntime fails the task layer (agent_inbox_event)
-- for the prior incarnation's in-flight work, but a delivery for that same
-- agent can still be sitting in 'leased'/'processing' with its lease not
-- yet naturally expired (default 2 minutes, see migration 160). Until then,
-- leaseAgentInboxEventForRuntime's same-agent serialization check (any
-- unexpired active_delivery for the agent blocks a new lease) also blocks
-- the fresh retry task this recovery just created — even though the daemon
-- is alive and polling normally. The result: retry looks stuck for up to
-- ~2 minutes with no error, no log, nothing to indicate why.
--
-- Scoping to runtime_id = $1 (not agent_id) keeps this legitimate: recovery
-- only runs for a runtime the server has just judged dead/restarted, so
-- every delivery still attributed to it is stale by definition. Do not
-- reuse this for any path other than orphan recovery — a live runtime's
-- in-progress delivery must never be touched by this.
UPDATE agent_event_delivery
SET status = 'expired',
    last_error = 'daemon restarted while event was in flight',
    updated_at = now()
WHERE runtime_id = $1
  AND status IN ('leased', 'processing');

-- name: RenewAgentInboxDelivery :one
UPDATE agent_event_delivery d
SET lease_expires_at = now() + interval '2 minutes',
    updated_at = now()
WHERE d.id = $1
  AND d.inbox_event_id = $2
  AND d.lease_token = $3
  AND d.status IN ('leased', 'processing')
  AND EXISTS (
    SELECT 1
    FROM agent_inbox_event e
    WHERE e.id = d.inbox_event_id
      AND e.agent_session_id = d.agent_session_id
      AND e.status = 'draining'
  )
  AND NOT EXISTS (
    SELECT 1
    FROM agent_event_delivery newer
    WHERE newer.inbox_event_id = d.inbox_event_id
      AND newer.id <> d.id
      AND newer.created_at >= d.created_at
  )
RETURNING *;

-- name: LockActiveAgentInboxDelivery :one
-- Provider start and non-chat terminal writes take this fence inside the same
-- transaction as the event mutation. Reclaim cannot slip between a read-only
-- lease check and the canonical write boundary.
SELECT d.runtime_id
FROM agent_event_delivery d
JOIN agent_inbox_event e
  ON e.id = d.inbox_event_id
 AND e.agent_session_id = d.agent_session_id
WHERE d.id = $1
  AND d.inbox_event_id = $2
  AND d.lease_token = $3
  AND d.status IN ('leased', 'processing')
  AND d.lease_expires_at > now()
  AND e.status = 'draining'
  AND (
    e.issue_run_kind IS NULL
    OR EXISTS (
      SELECT 1
      FROM active_issue_execution claim
      JOIN issue issue_row
        ON issue_row.workspace_id = claim.workspace_id
       AND issue_row.id = claim.issue_id
       AND issue_row.execution_revision = claim.issue_execution_revision
       AND issue_row.status IN ('todo', 'in_progress')
       AND issue_row.assignee_type = 'agent'
       AND issue_row.assignee_id = claim.agent_id
      WHERE claim.workspace_id = e.workspace_id
        AND claim.issue_id = e.issue_id
        AND claim.run_id = e.id
        AND claim.agent_id = e.agent_id
        AND claim.issue_execution_revision = e.issue_execution_revision
        AND claim.attempt_number = e.issue_execution_attempt_number
        AND claim.status = 'active'
    )
  )
  AND NOT EXISTS (
    SELECT 1
    FROM agent_event_delivery newer
    WHERE newer.inbox_event_id = d.inbox_event_id
      AND newer.id <> d.id
      AND newer.created_at >= d.created_at
  )
FOR UPDATE OF d, e;

-- name: LockCurrentAgentInboxDelivery :one
-- Terminal reports may arrive after their lease timestamp, matching the ACK
-- contract below, but only while no newer delivery has reclaimed the event.
SELECT d.runtime_id
FROM agent_event_delivery d
JOIN agent_inbox_event e
  ON e.id = d.inbox_event_id
 AND e.agent_session_id = d.agent_session_id
WHERE d.id = $1
  AND d.inbox_event_id = $2
  AND d.lease_token = $3
  AND d.status IN ('leased', 'processing')
  AND e.status = 'draining'
  AND NOT EXISTS (
    SELECT 1
    FROM agent_event_delivery newer
    WHERE newer.inbox_event_id = d.inbox_event_id
      AND newer.id <> d.id
      AND newer.created_at >= d.created_at
  )
FOR UPDATE OF d, e;

-- name: AckAgentInboxDelivery :one
WITH active_delivery AS (
  UPDATE agent_event_delivery d
  SET status = 'acked',
      acked_at = now(),
      updated_at = now()
  WHERE d.id = $1
    AND d.inbox_event_id = $2
    AND d.lease_token = $3
    AND d.status IN ('leased', 'processing')
    AND (
      d.lease_expires_at > now()
      OR NOT EXISTS (
        SELECT 1
        FROM agent_event_delivery newer
        WHERE newer.inbox_event_id = d.inbox_event_id
          AND newer.id <> d.id
          AND newer.created_at >= d.created_at
      )
    )
    AND EXISTS (
      SELECT 1
      FROM agent_inbox_event e
      WHERE e.id = d.inbox_event_id
        AND e.agent_session_id = d.agent_session_id
        AND e.status IN ('pending', 'draining', 'failed', 'acked')
    )
  RETURNING d.*
),
acked_event AS (
  UPDATE agent_inbox_event e
  SET status = 'acked',
      acked_at = now(),
      updated_at = now()
  FROM active_delivery d
  WHERE e.id = d.inbox_event_id
    AND e.agent_session_id = d.agent_session_id
    AND e.status IN ('pending', 'draining', 'failed', 'acked')
  RETURNING e.*
),
acked_session AS (
UPDATE agent_session s
SET last_drained_seq = GREATEST(s.last_drained_seq, acked_event.seq_to),
    last_acked_event_id = acked_event.id,
    updated_at = now()
FROM acked_event
WHERE s.id = acked_event.agent_session_id
RETURNING s.id
)
SELECT acked_event.*
FROM acked_event
JOIN acked_session ON acked_session.id = acked_event.agent_session_id;

-- name: SetAgentInboxTerminalOutcome :one
UPDATE agent_inbox_event
SET terminal_outcome = $3,
    terminal_delivery_id = $4,
    retryable = $5,
    terminal_at = now(),
    completed_at = COALESCE(completed_at, now()),
    updated_at = now()
WHERE id = $1
  AND workspace_id = $2
RETURNING *;

-- name: RetryAgentInboxEvent :one
WITH original AS (
  SELECT e.*
  FROM agent_inbox_event e
  WHERE e.id = $1
    AND e.workspace_id = $2
    AND e.channel_id = $3
    AND e.terminal_outcome = 'failed'
    AND e.retryable = true
    AND e.status = 'acked'
  FOR UPDATE
),
guarded AS (
  SELECT original.*
  FROM original
  WHERE (
      EXISTS (
        SELECT 1
        FROM chat_message prompt
        WHERE prompt.task_id = original.id
          AND prompt.role = 'user'
      )
      OR (
        original.chat_session_id IS NULL
        AND COALESCE(original.context->>'type', '') = 'channel_wake'
        AND COALESCE(original.context->>'prompt', '') <> ''
      )
    )
    AND NOT EXISTS (
    SELECT 1
    FROM agent_inbox_event newer
    WHERE newer.workspace_id = original.workspace_id
      AND newer.channel_id = original.channel_id
      AND newer.source_message_id = original.source_message_id
      AND newer.agent_id = original.agent_id
      AND newer.requires_wake = true
      AND newer.status IN ('pending', 'draining', 'failed')
      AND newer.created_at > COALESCE(original.terminal_at, original.updated_at)
  )
),
refreshed_session AS (
  UPDATE agent_session s
  SET runtime_id = a.runtime_id,
      channel_id = COALESCE(guarded.channel_id, s.channel_id),
      chat_session_id = COALESCE(guarded.chat_session_id, s.chat_session_id),
      status = 'active',
      updated_at = now()
  FROM guarded
  JOIN agent a ON a.id = guarded.agent_id
  WHERE s.id = guarded.agent_session_id
  RETURNING s.id
),
retried_event AS (
  INSERT INTO agent_inbox_event (
    workspace_id,
    agent_session_id,
    conversation_id,
    channel_id,
    chat_session_id,
    agent_id,
    runtime_id,
    execution_config,
    source_message_id,
    reason,
    requires_wake,
    status,
    priority,
    seq_from,
    seq_to,
    context,
    initiator_user_id
  )
  SELECT
    guarded.workspace_id,
    refreshed_session.id,
    guarded.conversation_id,
    guarded.channel_id,
    guarded.chat_session_id,
    guarded.agent_id,
    a.runtime_id,
    jsonb_build_object(
      'model', COALESCE(a.model, ''),
      'thinking_level', COALESCE(a.thinking_level, ''),
      'snapshotted', true
    ),
    guarded.source_message_id,
    guarded.reason,
    true,
    'pending',
    guarded.priority,
    guarded.seq_from,
    guarded.seq_to,
    guarded.context,
    guarded.initiator_user_id
  FROM guarded
  JOIN agent a ON a.id = guarded.agent_id
  JOIN refreshed_session ON refreshed_session.id = guarded.agent_session_id
  RETURNING *
),
copied_prompt AS (
  INSERT INTO chat_message (
    chat_session_id,
    role,
    content,
    parts,
    task_id,
    thread_id,
    channel_thread_root_message_id,
    trigger_depth
  )
  SELECT
    prompt.chat_session_id,
    prompt.role,
    prompt.content,
    prompt.parts,
    retried_event.id,
    prompt.thread_id,
    prompt.channel_thread_root_message_id,
    prompt.trigger_depth
  FROM chat_message prompt
  JOIN original ON prompt.task_id = original.id
  CROSS JOIN retried_event
  WHERE prompt.role = 'user'
    AND original.chat_session_id IS NOT NULL
  RETURNING id
)
SELECT retried_event.*
FROM retried_event
WHERE EXISTS (SELECT 1 FROM copied_prompt)
   OR EXISTS (
     SELECT 1
     FROM original
     WHERE original.chat_session_id IS NULL
       AND COALESCE(original.context->>'type', '') = 'channel_wake'
       AND COALESCE(original.context->>'prompt', '') <> ''
   );

-- name: FailAgentInboxDelivery :one
WITH active_delivery AS (
  UPDATE agent_event_delivery d
  SET status = 'failed',
      last_error = $3,
      updated_at = now()
  WHERE d.id = $1
    AND d.inbox_event_id = $2
    AND d.lease_token = $4
    AND d.status IN ('leased', 'processing')
    AND (
      d.lease_expires_at > now()
      OR NOT EXISTS (
        SELECT 1
        FROM agent_event_delivery newer
        WHERE newer.inbox_event_id = d.inbox_event_id
          AND newer.id <> d.id
          AND newer.created_at >= d.created_at
      )
    )
    AND EXISTS (
      SELECT 1
      FROM agent_inbox_event e
      WHERE e.id = d.inbox_event_id
        AND e.agent_session_id = d.agent_session_id
        AND e.status IN ('pending', 'draining')
    )
  RETURNING d.*
),
failed_event AS (
  UPDATE agent_inbox_event e
  SET status = 'failed',
      last_error = $3,
      updated_at = now()
  FROM active_delivery d
  WHERE e.id = d.inbox_event_id
    AND e.agent_session_id = d.agent_session_id
    AND e.status IN ('pending', 'draining')
  RETURNING e.*
)
SELECT * FROM failed_event;
