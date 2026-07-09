-- name: CreateTrainingDispatch :exec
INSERT INTO training_dispatch (project_id, workspace_id, train_agent_id, critic_agent_id, default_reward)
VALUES ($1, $2, $3, $4, COALESCE($5, 1.0))
ON CONFLICT (project_id) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  train_agent_id = EXCLUDED.train_agent_id,
  critic_agent_id = EXCLUDED.critic_agent_id,
  default_reward = EXCLUDED.default_reward;

-- name: GetTrainingDispatchByProject :one
SELECT * FROM training_dispatch
WHERE project_id = $1;

-- name: FindCriticTaskForTrained :one
-- Finds a critic task already spawned for a trained task. Used by the
-- critic-spawn hook's idempotency guard: if a row exists, the spawn is
-- skipped. Matches on context.critic_of.trained_task_id. Returns the
-- critic task's row (LIMIT 1) or no rows.
SELECT * FROM agent_task_queue
WHERE context->'critic_of'->>'trained_task_id' = $1::text
LIMIT 1;

-- name: CreateCriticTask :one
-- Inserts a critic task peer (parent_task_id NOT set) carrying the
-- critic_of linkage + trained_output in context JSONB. status is hardcoded
-- 'queued' so the daemon's normal claim path picks it up. issue_id is
-- inherited from the trained task so the critic shows up on the same issue.
INSERT INTO agent_task_queue (agent_id, issue_id, status, priority, context)
VALUES ($1, $2, 'queued', $3, $4)
RETURNING *;
