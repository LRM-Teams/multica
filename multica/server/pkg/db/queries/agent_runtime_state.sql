-- name: EnsureAgentRuntimeState :one
-- Creates the empty canonical row only for the agent's currently bound
-- runtime. Existing rows are returned without a no-op UPDATE so polling does
-- not create row churn.
WITH current_agent AS (
    SELECT current_agent_row.id, current_agent_row.runtime_id
    FROM agent current_agent_row
    WHERE current_agent_row.id = sqlc.arg('agent_id')
      AND current_agent_row.runtime_id = sqlc.arg('runtime_id')
),
inserted AS (
    INSERT INTO agent_runtime_state (agent_id, runtime_id)
    SELECT current.id, current.runtime_id
    FROM current_agent current
    ON CONFLICT (agent_id, runtime_id) DO NOTHING
    RETURNING *
)
SELECT * FROM inserted
UNION ALL
SELECT state.*
FROM agent_runtime_state state
JOIN current_agent current
  ON current.id = state.agent_id
 AND current.runtime_id = state.runtime_id
WHERE NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1;

-- name: GetAgentRuntimeState :one
SELECT *
FROM agent_runtime_state
WHERE agent_id = sqlc.arg('agent_id')
  AND runtime_id = sqlc.arg('runtime_id');

-- name: GetCurrentAgentRuntimeState :one
SELECT state.*
FROM agent_runtime_state state
JOIN agent current
  ON current.id = state.agent_id
 AND current.runtime_id = state.runtime_id
WHERE state.agent_id = sqlc.arg('agent_id')
  AND state.runtime_id = sqlc.arg('runtime_id');

-- name: AdvanceAgentRuntimeStateCAS :one
-- generation is the row's compare-and-swap token. Every accepted turn bumps
-- it, so a late result from an older turn cannot overwrite a newer provider
-- session. Nullable pointer inputs preserve their current canonical values.
-- The first-wake notice is consumed only after a real provider session exists;
-- a failed first wake therefore sees the notice again on retry.
-- A reset is the one deliberate same-turn successor: the daemon may discover
-- a poisoned resume, clear it, then establish a fresh provider session during
-- the same wake. That successor must present the generation produced by the
-- clear, and only an empty row carrying the reset notice qualifies. Generation
-- CAS still rejects the pre-clear writer and all concurrent late results.
UPDATE agent_runtime_state state
SET provider_session_id = COALESCE(
        NULLIF(btrim(sqlc.narg('provider_session_id')::text), ''),
        state.provider_session_id
    ),
    work_dir = COALESCE(
        NULLIF(btrim(sqlc.narg('work_dir')::text), ''),
        state.work_dir
    ),
    provider_config_fingerprint = COALESCE(
        NULLIF(btrim(sqlc.narg('provider_config_fingerprint')::text), ''),
        state.provider_config_fingerprint
    ),
    generation = state.generation + 1,
    last_turn_id = sqlc.arg('turn_id')::uuid,
    fresh_session_notice_reason = CASE
        WHEN COALESCE(
            NULLIF(btrim(sqlc.narg('provider_session_id')::text), ''),
            state.provider_session_id
        ) IS NOT NULL THEN NULL
        ELSE state.fresh_session_notice_reason
    END,
    updated_at = now()
WHERE state.agent_id = sqlc.arg('agent_id')
  AND state.runtime_id = sqlc.arg('runtime_id')
  AND state.generation = sqlc.arg('expected_generation')
  AND (
      state.last_turn_id IS NULL
      OR state.last_turn_id <> sqlc.arg('turn_id')::uuid
      OR (
          state.last_turn_id = sqlc.arg('turn_id')::uuid
          AND state.provider_session_id IS NULL
          AND state.fresh_session_notice_reason = 'reset'
          AND NULLIF(btrim(sqlc.narg('provider_session_id')::text), '') IS NOT NULL
      )
  )
  AND EXISTS (
      SELECT 1
      FROM agent current
      WHERE current.id = state.agent_id
        AND current.runtime_id = state.runtime_id
  )
RETURNING state.*;

-- name: ClearAgentRuntimeSessionCAS :one
-- Invalid/poisoned provider state may clear only the generation it actually
-- observed. The stable agent workdir is intentionally preserved. Migration A
-- owns the one-time cutover notice; this runtime invalidation path accepts
-- reset only.
UPDATE agent_runtime_state state
SET provider_session_id = NULL,
    provider_config_fingerprint = NULL,
    generation = state.generation + 1,
    last_turn_id = sqlc.arg('turn_id')::uuid,
    fresh_session_notice_reason = sqlc.arg('notice_reason')::text,
    updated_at = now()
WHERE state.agent_id = sqlc.arg('agent_id')
  AND state.runtime_id = sqlc.arg('runtime_id')
  AND state.generation = sqlc.arg('expected_generation')
  AND sqlc.arg('notice_reason')::text = 'reset'
  AND (state.last_turn_id IS NULL OR state.last_turn_id <> sqlc.arg('turn_id')::uuid)
  AND EXISTS (
      SELECT 1
      FROM agent current
      WHERE current.id = state.agent_id
        AND current.runtime_id = state.runtime_id
  )
RETURNING state.*;
