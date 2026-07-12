-- name: CreateAgentRadarRun :one
INSERT INTO agent_radar_run (
    workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
    status, cooldown_key, context_summary, scheduled_for
) VALUES (
    $1, $2, sqlc.narg('runtime_id'), $3, $4,
    COALESCE(sqlc.narg('status'), 'planned'), $5, $6, COALESCE(sqlc.narg('scheduled_for'), now())
)
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
FROM agent_radar_run
WHERE workspace_id = $1
  AND agent_id = $2
  AND created_at >= $3
  AND status IN ('planned', 'queued', 'running', 'succeeded', 'no_action');

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
