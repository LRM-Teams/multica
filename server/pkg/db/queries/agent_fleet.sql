-- Agent fleet rank snapshots and scoring aggregates.

-- name: ListWorkspaceActiveAgentIDs :many
SELECT id FROM agent
WHERE workspace_id = $1 AND archived_at IS NULL
ORDER BY created_at;

-- name: GetFleetDeliveryStats :many
SELECT
    atq.agent_id,
    COUNT(*) FILTER (WHERE atq.terminal_outcome = 'completed')::bigint AS completed_count,
    COUNT(*) FILTER (WHERE atq.terminal_outcome = 'failed')::bigint AS failed_count
FROM agent_inbox_event atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = @workspace_id
  AND atq.status = 'acked'
  AND atq.terminal_outcome IN ('completed', 'failed')
  AND atq.completed_at > now() - make_interval(days => @window_days::int)
  AND COALESCE(atq.context->>'type', '') <> 'agent_radar'
GROUP BY atq.agent_id;

-- name: GetFleetEvolutionFeedbackStats :many
SELECT
    agent_id,
    COUNT(*) FILTER (WHERE event = 'success')::bigint AS success_count,
    COUNT(*) FILTER (WHERE event IN ('success', 'failure'))::bigint AS feedback_total
FROM evolution_unit_feedback_event
WHERE workspace_id = @workspace_id
  AND agent_id IS NOT NULL
  AND created_at > now() - make_interval(days => @window_days::int)
GROUP BY agent_id;

-- name: GetFleetEvolutionPromotionStats :many
SELECT
    source_agent_id AS agent_id,
    COUNT(*)::bigint AS promotion_count
FROM evolution_unit_submission
WHERE workspace_id = @workspace_id
  AND status = 'promoted'
  AND updated_at > now() - make_interval(days => @window_days::int)
GROUP BY source_agent_id;

-- name: GetFleetGrowthStats :many
SELECT
    amwe.agent_id,
    COUNT(*) FILTER (WHERE amwe.created_at > now() - make_interval(days => @window_days::int))::bigint AS writes_30d,
    COUNT(*)::bigint AS total_writes
FROM agent_memory_write_event amwe
JOIN agent a ON a.id = amwe.agent_id
WHERE a.workspace_id = @workspace_id
GROUP BY amwe.agent_id;

-- name: GetFleetEfficiencyStats :many
SELECT
    atq.agent_id,
    COUNT(*)::bigint AS completed_count,
    COALESCE(
        SUM(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at))),
        0
    )::float8 AS total_seconds,
    COALESCE(
        (
            SELECT SUM(usage.input_tokens + usage.output_tokens)::bigint
            FROM agent_usage usage
            JOIN agent_execution execution ON execution.id = usage.execution_id
            JOIN agent_inbox_event completed_event
              ON execution.source_kind = 'inbox'
             AND completed_event.id = execution.source_event_id
            WHERE execution.workspace_id = @workspace_id
              AND execution.agent_id = atq.agent_id
              AND completed_event.status = 'acked'
              AND completed_event.terminal_outcome = 'completed'
              AND completed_event.completed_at > now() - make_interval(days => @window_days::int)
              AND COALESCE(completed_event.context->>'type', '') <> 'agent_radar'
        ),
        0
    )::bigint AS total_tokens
FROM agent_inbox_event atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = @workspace_id
  AND atq.status = 'acked'
  AND atq.terminal_outcome = 'completed'
  AND atq.started_at IS NOT NULL
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at > now() - make_interval(days => @window_days::int)
  AND COALESCE(atq.context->>'type', '') <> 'agent_radar'
GROUP BY atq.agent_id;

-- name: ListAgentFleetSnapshots :many
SELECT snapshot.*
FROM agent_fleet_snapshot snapshot
JOIN agent a ON a.id = snapshot.agent_id
WHERE snapshot.workspace_id = $1
  AND a.archived_at IS NULL
  AND snapshot.frozen = false
ORDER BY snapshot.fleet_rank ASC, snapshot.fleet_score DESC;

-- name: GetAgentFleetSnapshot :one
SELECT * FROM agent_fleet_snapshot
WHERE workspace_id = $1 AND agent_id = $2;

-- name: UpsertAgentFleetSnapshot :exec
INSERT INTO agent_fleet_snapshot (
    workspace_id, agent_id, fleet_score, class_id, fleet_rank, fleet_size,
    sample_tasks, pillar_delivery, pillar_evolution, pillar_growth, pillar_efficiency,
    computed_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now()
)
ON CONFLICT (workspace_id, agent_id) DO UPDATE SET
    fleet_score = EXCLUDED.fleet_score,
    class_id = EXCLUDED.class_id,
    fleet_rank = EXCLUDED.fleet_rank,
    fleet_size = EXCLUDED.fleet_size,
    sample_tasks = EXCLUDED.sample_tasks,
    pillar_delivery = EXCLUDED.pillar_delivery,
    pillar_evolution = EXCLUDED.pillar_evolution,
    pillar_growth = EXCLUDED.pillar_growth,
    pillar_efficiency = EXCLUDED.pillar_efficiency,
    computed_at = now()
WHERE agent_fleet_snapshot.frozen = false;

-- name: FreezeAgentFleetSnapshot :exec
UPDATE agent_fleet_snapshot
SET frozen = true, frozen_at = now()
WHERE workspace_id = $1 AND agent_id = $2;

-- name: UnfreezeAgentFleetSnapshot :exec
UPDATE agent_fleet_snapshot
SET frozen = false, frozen_at = NULL
WHERE workspace_id = $1 AND agent_id = $2;

-- name: InsertAgentFleetHistory :one
INSERT INTO agent_fleet_history (
    workspace_id, agent_id, fleet_score, class_id, fleet_rank, fleet_size,
    sample_tasks, pillar_delivery, pillar_evolution, pillar_growth, pillar_efficiency,
    trigger_reason
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: ListAgentFleetHistory :many
SELECT * FROM agent_fleet_history
WHERE workspace_id = $1 AND agent_id = $2
ORDER BY recorded_at DESC
LIMIT $3;
