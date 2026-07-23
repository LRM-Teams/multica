-- name: CreateVoiceCallSession :one
INSERT INTO voice_call_session (
  workspace_id,
  channel_id,
  agent_id,
  user_id,
  provider,
  provider_task_id,
  room_id,
  status
) VALUES (
  sqlc.arg('workspace_id'),
  sqlc.arg('channel_id'),
  sqlc.arg('agent_id'),
  sqlc.arg('user_id'),
  sqlc.arg('provider'),
  sqlc.arg('provider_task_id'),
  sqlc.arg('room_id'),
  'starting'
)
RETURNING *;

-- name: GetVoiceCallSessionForMember :one
SELECT *
FROM voice_call_session
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND user_id = sqlc.arg('user_id');

-- name: MarkVoiceCallConnecting :one
UPDATE voice_call_session
SET
  status = 'connecting',
  updated_at = now()
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status = 'starting'
RETURNING *;

-- name: MarkVoiceCallFailed :one
UPDATE voice_call_session
SET
  status = 'failed',
  ended_at = now(),
  error_code = sqlc.arg('error_code'),
  updated_at = now()
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status NOT IN ('ended', 'failed')
RETURNING *;

-- name: BeginVoiceCallEnding :one
WITH current_session AS (
  SELECT session.id, session.status
  FROM voice_call_session AS session
  WHERE session.id = sqlc.arg('id')
    AND session.workspace_id = sqlc.arg('workspace_id')
    AND session.user_id = sqlc.arg('user_id')
  FOR UPDATE
),
updated AS (
  UPDATE voice_call_session AS call
  SET
    status = CASE
      WHEN current_session.status IN ('ended', 'failed', 'ending') THEN call.status
      ELSE 'ending'
    END,
    end_reason = CASE
      WHEN current_session.status IN ('ended', 'failed', 'ending') THEN call.end_reason
      ELSE sqlc.arg('end_reason')
    END,
    updated_at = CASE
      WHEN current_session.status IN ('ended', 'failed') THEN call.updated_at
      ELSE now()
    END
  FROM current_session
  WHERE call.id = current_session.id
  RETURNING
    call.*,
    current_session.status NOT IN ('ended', 'failed') AS provider_stop_required
)
SELECT *
FROM updated;

-- name: MarkVoiceCallEnded :one
UPDATE voice_call_session
SET
  status = 'ended',
  ended_at = now(),
  end_reason = sqlc.arg('end_reason'),
  updated_at = now()
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status = 'ending'
RETURNING *;
