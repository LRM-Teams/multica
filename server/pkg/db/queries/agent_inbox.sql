-- name: UpsertAgentSession :one
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
  source_message_id,
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
  sqlc.narg('source_message_id'),
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
  source_message_id,
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
  $4,
  $5,
  sqlc.narg('source_message_id'),
  'ambient',
  false,
  'pending',
  0,
  $6,
  $7
)
ON CONFLICT (conversation_id, agent_id)
  WHERE reason = 'ambient' AND status IN ('pending', 'failed') AND conversation_id IS NOT NULL
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
SELECT count(*)
FROM agent_inbox_event e
JOIN agent_session s ON s.id = e.agent_session_id
WHERE s.runtime_id = $1
  AND s.status = 'active'
  AND e.status IN ('pending', 'failed');

-- name: ReclaimExpiredAgentInboxDeliveriesForRuntime :exec
WITH expired_delivery AS (
  UPDATE agent_event_delivery d
  SET status = 'expired',
      last_error = 'delivery lease expired',
      updated_at = now()
  FROM agent_session s, agent_inbox_event e
  WHERE d.agent_session_id = s.id
    AND d.inbox_event_id = e.id
    AND s.runtime_id = $1
    AND s.status = 'active'
    AND e.status = 'draining'
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
  AND NOT EXISTS (
    SELECT 1
    FROM agent_event_delivery d
    WHERE d.inbox_event_id = e.id
      AND d.status IN ('leased', 'processing')
      AND d.lease_expires_at > now()
  );

-- name: LeaseAgentInboxEventForRuntime :one
WITH next_event AS (
  SELECT e.id
  FROM agent_inbox_event e
  JOIN agent_session s ON s.id = e.agent_session_id
  WHERE s.runtime_id = $1
    AND s.status = 'active'
    AND e.status IN ('pending', 'failed')
  ORDER BY e.priority DESC, e.requires_wake DESC, e.created_at ASC, e.id ASC
  LIMIT 1
  FOR UPDATE SKIP LOCKED
),
leased_event AS (
  UPDATE agent_inbox_event e
  SET status = 'draining',
      claimed_at = now(),
      attempt = attempt + 1,
      updated_at = now()
  FROM next_event
  WHERE e.id = next_event.id
  RETURNING e.*
)
INSERT INTO agent_event_delivery (
  workspace_id,
  agent_session_id,
  inbox_event_id,
  runtime_id,
  status
)
SELECT
  e.workspace_id,
  e.agent_session_id,
  e.id,
  $1,
  'leased'
FROM leased_event e
RETURNING *;

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
        AND e.status IN ('pending', 'draining', 'failed')
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
    AND e.status IN ('pending', 'draining', 'failed')
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
