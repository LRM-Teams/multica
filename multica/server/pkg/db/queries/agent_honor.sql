-- Durable agent honor state, event ledger, achievements, rules, and audit.

-- name: CreateAgentHonorStateIfMissing :one
INSERT INTO agent_honor_state (workspace_id, agent_id)
VALUES ($1, $2)
ON CONFLICT (workspace_id, agent_id) DO UPDATE
SET updated_at = agent_honor_state.updated_at
RETURNING *;

-- name: GetAgentHonorState :one
SELECT * FROM agent_honor_state
WHERE workspace_id = $1 AND agent_id = $2;

-- name: SumAgentHonorXP :one
SELECT GREATEST(0, COALESCE(SUM(xp_delta), 0))::bigint AS total_xp
FROM agent_honor_event
WHERE workspace_id = $1 AND agent_id = $2;

-- name: UpdateAgentHonorStats :one
UPDATE agent_honor_state
SET total_xp = $3, level = $4, updated_at = now()
WHERE workspace_id = $1 AND agent_id = $2
RETURNING *;

-- name: InsertAgentHonorEventIfNew :one
INSERT INTO agent_honor_event (
    workspace_id, agent_id, event_type, source_ref, xp_delta, reason, metadata, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (workspace_id, agent_id, event_type, source_ref) DO NOTHING
RETURNING *;

-- name: BackfillAgentDeliveryHonorEvents :many
INSERT INTO agent_honor_event (
    workspace_id, agent_id, event_type, source_ref, xp_delta, reason, created_at
)
SELECT
    e.workspace_id,
    e.agent_id,
    'delivery',
    e.id::text,
    $3,
    'Accepted delivery',
    COALESCE(e.completed_at, e.terminal_at, e.updated_at)
FROM agent_inbox_event e
WHERE e.workspace_id = $1
  AND e.agent_id = $2
  AND e.status = 'acked'
  AND e.terminal_outcome = 'completed'
  AND COALESCE(e.context->>'type', '') <> 'agent_radar'
ON CONFLICT DO NOTHING
RETURNING *;

-- name: ListRecentAgentHonorEvents :many
SELECT * FROM agent_honor_event
WHERE workspace_id = $1 AND agent_id = $2
ORDER BY created_at DESC
LIMIT $3;

-- name: GetAgentHonorMetrics :one
SELECT
    (
        SELECT COUNT(*) FROM agent_inbox_event e
        WHERE e.workspace_id = $1
          AND e.agent_id = $2
          AND e.status = 'acked'
          AND e.terminal_outcome = 'completed'
          AND COALESCE(e.context->>'type', '') <> 'agent_radar'
    )::bigint AS completed_count,
    (
        SELECT COUNT(*) FROM agent_inbox_event e
        WHERE e.workspace_id = $1
          AND e.agent_id = $2
          AND e.status = 'acked'
          AND e.terminal_outcome = 'failed'
          AND COALESCE(e.context->>'type', '') <> 'agent_radar'
    )::bigint AS failed_count,
    (
        SELECT COUNT(*) FROM agent_memory_write_event m
        WHERE m.workspace_id = $1 AND m.agent_id = $2
    )::bigint AS memory_writes,
    (
        SELECT COUNT(*) FROM evolution_unit_submission s
        WHERE s.workspace_id = $1
          AND s.source_agent_id = $2
          AND s.status = 'promoted'
    )::bigint AS evolution_promotions,
    (
        SELECT COUNT(DISTINCT i.project_id)
        FROM agent_inbox_event e
        JOIN issue i ON i.id = e.issue_id
        WHERE e.workspace_id = $1
          AND e.agent_id = $2
          AND e.status = 'acked'
          AND e.terminal_outcome = 'completed'
          AND i.project_id IS NOT NULL
    )::bigint AS distinct_projects,
    (
        SELECT COUNT(DISTINCT completed.issue_id)
        FROM agent_inbox_event completed
        WHERE completed.workspace_id = $1
          AND completed.agent_id = $2
          AND completed.status = 'acked'
          AND completed.terminal_outcome = 'completed'
          AND completed.issue_id IS NOT NULL
          AND EXISTS (
              SELECT 1 FROM agent_inbox_event failed
              WHERE failed.agent_id = completed.agent_id
                AND failed.issue_id = completed.issue_id
                AND failed.status = 'acked'
                AND failed.terminal_outcome = 'failed'
                AND failed.created_at < completed.created_at
          )
    )::bigint AS recovery_count;

-- name: ListAgentRecentTerminalOutcomes :many
SELECT terminal_outcome
FROM agent_inbox_event
WHERE workspace_id = $1
  AND agent_id = $2
  AND status = 'acked'
  AND terminal_outcome IN ('completed', 'failed')
  AND COALESCE(context->>'type', '') <> 'agent_radar'
ORDER BY COALESCE(completed_at, terminal_at, updated_at) DESC
LIMIT $3;

-- name: InsertAgentHonorUnlockIfNew :one
INSERT INTO agent_honor_unlock (workspace_id, agent_id, achievement_id, source)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, agent_id, achievement_id) DO NOTHING
RETURNING *;

-- name: ListAgentHonorUnlocks :many
SELECT * FROM agent_honor_unlock
WHERE workspace_id = $1 AND agent_id = $2
ORDER BY unlocked_at DESC;

-- name: DeleteAgentHonorUnlock :execrows
DELETE FROM agent_honor_unlock
WHERE workspace_id = $1 AND agent_id = $2 AND achievement_id = $3;

-- name: DeleteAgentAchievementHonorEvent :exec
DELETE FROM agent_honor_event
WHERE workspace_id = $1
  AND agent_id = $2
  AND event_type = 'achievement'
  AND source_ref = $3;

-- name: CountAgentAchievementUnlocks :many
SELECT achievement_id, COUNT(*)::bigint AS unlock_count
FROM agent_honor_unlock
GROUP BY achievement_id;

-- name: CountAgentHonorParticipants :one
SELECT COUNT(*)::bigint
FROM agent_honor_state
WHERE total_xp > 0;

-- name: UpdateAgentHonorShowcase :one
UPDATE agent_honor_state
SET showcase_achievement_ids = $3,
    equipped_achievement_id = NULLIF($4, ''),
    updated_at = now()
WHERE workspace_id = $1 AND agent_id = $2
RETURNING *;

-- name: GetAgentHonorRuleConfig :one
SELECT * FROM agent_honor_rule_config
WHERE workspace_id = $1;

-- name: UpsertAgentHonorRuleConfig :one
INSERT INTO agent_honor_rule_config (workspace_id, version, config, updated_by)
VALUES ($1, 1, $2, $3)
ON CONFLICT (workspace_id) DO UPDATE SET
    version = agent_honor_rule_config.version + 1,
    config = EXCLUDED.config,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;

-- name: InsertAgentHonorAdminAudit :one
INSERT INTO agent_honor_admin_audit (
    workspace_id, agent_id, action, details, created_by
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListAgentHonorAdminAudit :many
SELECT * FROM agent_honor_admin_audit
WHERE workspace_id = $1
  AND ($2::uuid IS NULL OR agent_id = $2)
ORDER BY created_at DESC
LIMIT $3;
