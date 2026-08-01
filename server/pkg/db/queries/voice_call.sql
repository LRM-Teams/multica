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

-- name: UpsertVoiceCallProviderTurn :one
WITH target_session AS (
  SELECT id
  FROM voice_call_session
  WHERE provider = sqlc.arg('provider')
    AND provider_task_id = sqlc.arg('provider_task_id')
)
INSERT INTO voice_call_turn (
  call_session_id,
  sequence,
  speaker,
  transcript,
  started_at,
  ended_at,
  is_interrupted,
  provider_turn_id
)
SELECT
  target_session.id,
  sqlc.arg('sequence'),
  sqlc.arg('speaker'),
  sqlc.arg('transcript'),
  now(),
  now(),
  sqlc.arg('is_interrupted'),
  sqlc.arg('provider_turn_id')
FROM target_session
ON CONFLICT (call_session_id, provider_turn_id)
  WHERE provider_turn_id IS NOT NULL
DO UPDATE SET
  transcript = EXCLUDED.transcript,
  ended_at = GREATEST(voice_call_turn.ended_at, EXCLUDED.ended_at),
  is_interrupted = EXCLUDED.is_interrupted
RETURNING
  id,
  call_session_id,
  sequence,
  speaker,
  transcript,
  is_interrupted,
  provider_turn_id;

-- name: BeginVoiceCallProviderStart :one
WITH current_session AS (
  SELECT session.id, session.status
  FROM voice_call_session AS session
  WHERE session.id = sqlc.arg('id')
    AND session.workspace_id = sqlc.arg('workspace_id')
    AND session.status IN ('starting', 'connecting', 'active', 'reconnecting')
  FOR UPDATE
),
updated AS (
  UPDATE voice_call_session AS call
  SET
    status = CASE
      WHEN current_session.status = 'starting' THEN 'connecting'
      WHEN current_session.status = 'connecting' AND call.connected_at IS NOT NULL
        THEN 'active'
      ELSE call.status
    END,
    updated_at = CASE
      WHEN current_session.status = 'starting'
        OR (current_session.status = 'connecting' AND call.connected_at IS NOT NULL)
        THEN now()
      ELSE call.updated_at
    END
  FROM current_session
  WHERE call.id = current_session.id
  RETURNING
    call.*,
    current_session.status = 'starting' AS provider_start_required
)
SELECT *
FROM updated;

-- name: MarkVoiceCallFailed :one
UPDATE voice_call_session
SET
  status = CASE
    WHEN status IN ('ended', 'failed') THEN status
    ELSE 'failed'
  END,
  ended_at = CASE
    WHEN status IN ('ended', 'failed') THEN ended_at
    ELSE now()
  END,
  error_code = CASE
    WHEN status IN ('ended', 'failed') THEN error_code
    ELSE sqlc.arg('error_code')
  END,
  updated_at = CASE
    WHEN status IN ('ended', 'failed') THEN updated_at
    ELSE now()
  END
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;

-- name: ApplyVoiceCallProviderActive :one
UPDATE voice_call_session
SET
  status = CASE
    WHEN status = 'starting' THEN 'connecting'
    WHEN status IN ('connecting', 'reconnecting') THEN 'active'
    ELSE status
  END,
  connected_at = CASE
    WHEN status IN ('starting', 'connecting', 'reconnecting')
      THEN COALESCE(connected_at, now())
    ELSE connected_at
  END,
  updated_at = CASE
    WHEN status IN ('starting', 'connecting', 'reconnecting') THEN now()
    ELSE updated_at
  END
WHERE provider = sqlc.arg('provider')
  AND provider_task_id = sqlc.arg('provider_task_id')
RETURNING *;

-- Client-confirmed audible answer when Volcengine callbacks cannot reach the
-- public origin. Promotes starting/connecting/reconnecting to active and sets
-- connected_at; idempotent for an already-active session.
-- name: ApplyVoiceCallClientAnswered :one
UPDATE voice_call_session
SET
  status = CASE
    WHEN status IN ('starting', 'connecting', 'reconnecting') THEN 'active'
    ELSE status
  END,
  connected_at = CASE
    WHEN status IN ('starting', 'connecting', 'reconnecting', 'active')
      THEN COALESCE(connected_at, now())
    ELSE connected_at
  END,
  updated_at = CASE
    WHEN status IN ('starting', 'connecting', 'reconnecting')
      OR (status = 'active' AND connected_at IS NULL)
      THEN now()
    ELSE updated_at
  END
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND user_id = sqlc.arg('user_id')
  AND status IN ('starting', 'connecting', 'active', 'reconnecting')
RETURNING *;

-- name: ApplyVoiceCallProviderFailure :one
UPDATE voice_call_session
SET
  status = CASE
    WHEN status IN ('ended', 'failed') THEN status
    ELSE 'failed'
  END,
  ended_at = CASE
    WHEN status IN ('ended', 'failed') THEN ended_at
    ELSE now()
  END,
  error_code = CASE
    WHEN status IN ('ended', 'failed') THEN error_code
    ELSE sqlc.arg('error_code')
  END,
  updated_at = CASE
    WHEN status IN ('ended', 'failed') THEN updated_at
    ELSE now()
  END
WHERE provider = sqlc.arg('provider')
  AND provider_task_id = sqlc.arg('provider_task_id')
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
    current_session.status NOT IN ('starting', 'ended', 'failed') AS provider_stop_required
)
SELECT *
FROM updated;

-- name: MarkVoiceCallEnded :one
UPDATE voice_call_session
SET
  status = CASE
    WHEN status = 'ending' THEN 'ended'
    ELSE status
  END,
  ended_at = CASE
    WHEN status = 'ending' THEN COALESCE(ended_at, now())
    ELSE ended_at
  END,
  end_reason = CASE
    WHEN status = 'ending' THEN sqlc.arg('end_reason')
    ELSE end_reason
  END,
  updated_at = CASE
    WHEN status = 'ending' THEN now()
    ELSE updated_at
  END
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND status IN ('ending', 'ended', 'failed')
RETURNING *;
