-- name: CreateAgentRadarRun :one
INSERT INTO agent_radar_run (
    workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
    status, cooldown_key, context_summary, scheduled_for
) VALUES (
    $1, $2, sqlc.narg('runtime_id'), $3, $4,
    COALESCE(sqlc.narg('status'), 'planned'), $5, $6, COALESCE(sqlc.narg('scheduled_for'), now())
)
ON CONFLICT (workspace_id, agent_id)
WHERE status IN ('planned', 'queued', 'running')
DO NOTHING
RETURNING *;

-- name: GetAgentRadarRun :one
SELECT * FROM agent_radar_run
WHERE id = $1;

-- name: ListAgentRadarRunsByAgent :many
SELECT * FROM agent_radar_run
WHERE workspace_id = $1 AND agent_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListPlannedAgentRadarRuns :many
SELECT * FROM agent_radar_run
WHERE status = 'planned' AND scheduled_for <= now()
ORDER BY scheduled_for ASC, created_at ASC
LIMIT $1;

-- name: UpdateAgentRadarRunStatus :one
UPDATE agent_radar_run
SET
    status = $2,
    task_id = COALESCE(sqlc.narg('task_id'), task_id),
    action_plan = COALESCE(sqlc.narg('action_plan'), action_plan),
    error = COALESCE(sqlc.narg('error'), error),
    started_at = CASE WHEN $2 = 'running' AND started_at IS NULL THEN now() ELSE started_at END,
    finished_at = CASE WHEN $2 IN ('succeeded', 'no_action', 'failed', 'cancelled') THEN now() ELSE finished_at END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CountRecentAgentRadarRuns :one
SELECT count(*)::bigint
FROM agent_radar_run rr
LEFT JOIN agent_task_queue atq ON atq.id = rr.task_id
WHERE rr.workspace_id = $1
  AND rr.agent_id = $2
  AND rr.created_at >= $3
  -- Migration 167 closed invalid pre-existing runs so the active-run unique
  -- guard could be installed. Those rows never represented a new Radar
  -- attempt and must not consume the retry-storm budget.
  AND COALESCE(atq.failure_reason, '') NOT IN (
      'radar_active_run_repair',
      'radar_stale_dispatch_repair'
  )
  AND NOT (
      rr.status = 'failed'
      AND (
          COALESCE(rr.error, '') LIKE 'migration:%'
          OR COALESCE(rr.error, '') = 'radar_stale_dispatch_repair'
      )
  );

-- name: FailStaleDispatchedAgentRadarTasks :many
-- ReclaimAgentTask refreshes dispatched_at when it re-delivers a claim whose
-- start acknowledgement never arrived. Use the immutable creation age here
-- so repeated re-delivery cannot keep a Radar task active forever. The one-hour
-- threshold is deliberately much larger than the normal 90-second claim
-- recovery and five-minute dispatch timeout windows.
WITH victims AS MATERIALIZED (
    SELECT rr.id AS radar_run_id, atq.id AS task_id
    FROM agent_radar_run rr
    JOIN agent_task_queue atq ON atq.id = rr.task_id
    WHERE rr.status = 'queued'
      AND rr.started_at IS NULL
      AND rr.created_at < now() - make_interval(secs => @stale_age_secs::double precision)
      AND atq.status = 'dispatched'
      AND atq.started_at IS NULL
      AND atq.created_at < now() - make_interval(secs => @stale_age_secs::double precision)
      AND atq.agent_id = rr.agent_id
      -- Runtime consolidation can reassign the task before deleting the old
      -- runtime, which leaves rr.runtime_id stale or NULL. The task FK plus
      -- the context backpointer are the durable pair identity.
      AND atq.context->>'type' = 'agent_radar'
      AND atq.context->>'radar_run_id' = rr.id::text
    ORDER BY atq.created_at ASC, atq.id ASC
    LIMIT @max_per_tick::int
    FOR UPDATE OF rr, atq SKIP LOCKED
)
UPDATE agent_task_queue atq
SET
    status = 'failed',
    completed_at = now(),
    error = 'Radar task remained dispatched without starting',
    failure_reason = 'radar_stale_dispatch_repair'
FROM victims v
WHERE atq.id = v.task_id
  AND atq.status = 'dispatched'
  AND atq.started_at IS NULL
  AND atq.created_at < now() - make_interval(secs => @stale_age_secs::double precision)
RETURNING atq.*;

-- name: MarkAgentRadarRunRunningByTaskID :execrows
UPDATE agent_radar_run
SET
    status = 'running',
    started_at = COALESCE(started_at, now()),
    updated_at = now()
WHERE task_id = $1
  AND status IN ('planned', 'queued');

-- name: FailAgentRadarRunByTaskID :execrows
UPDATE agent_radar_run
SET
    status = 'failed',
    error = COALESCE(sqlc.narg('error'), error),
    finished_at = COALESCE(finished_at, now()),
    updated_at = now()
WHERE task_id = $1
  AND status IN ('planned', 'queued', 'running');

-- name: CancelAgentRadarRunByTaskID :execrows
UPDATE agent_radar_run
SET
    status = 'cancelled',
    error = COALESCE(sqlc.narg('error'), error),
    finished_at = COALESCE(finished_at, now()),
    updated_at = now()
WHERE task_id = $1
  AND status IN ('planned', 'queued', 'running');

-- name: CreateAgentRadarAction :one
INSERT INTO agent_radar_action (
    radar_run_id, workspace_id, agent_id, action_type, status, risk_level,
    confidence, dedupe_key, target_kind, target_id, reason, evidence, payload
) VALUES (
    $1, $2, $3, $4, COALESCE(sqlc.narg('status'), 'proposed'), $5,
    $6, $7, $8, sqlc.narg('target_id'), $9, $10, $11
)
ON CONFLICT (workspace_id, agent_id, dedupe_key)
WHERE dedupe_key <> '' AND status IN ('proposed', 'approved', 'executing', 'executed')
DO NOTHING
RETURNING *;

-- name: ListAgentRadarActionsByRun :many
SELECT * FROM agent_radar_action
WHERE radar_run_id = $1
ORDER BY created_at ASC, id ASC;

-- name: UpdateAgentRadarActionStatus :one
UPDATE agent_radar_action
SET
    status = $2,
    result = COALESCE(sqlc.narg('result'), result),
    error = COALESCE(sqlc.narg('error'), error),
    updated_at = now()
WHERE id = $1
RETURNING *;
