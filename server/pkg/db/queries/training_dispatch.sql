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

-- name: GetRootTrainingTaskStatusForProject :one
-- Resolves the status of the project's ROOT training task for the GET /dag
-- endpoint's 202-vs-200 decision (Task 9, U8). The root training task is the
-- agent_task_queue row created by the dispatch's EnqueueAgentRun: it shares
-- the dispatch's issue (issue.project_id = training_dispatch.project_id) and
-- its agent_id equals training_dispatch.train_agent_id (excluding any critic
-- task, which is spawned with critic_agent_id). One training target per
-- rollout project (training_dispatch.project_id is the PK), so the issue +
-- agent_id scope yields exactly the root task; ORDER BY created_at DESC
-- LIMIT 1 keeps this stable under a future re-dispatch. Returns pgx.ErrNoRows
-- when the project has no training_dispatch or no root task has been enqueued
-- yet (caller treats both as "in_progress": the rollout is not done).
SELECT atq.status
FROM training_dispatch td
JOIN issue i ON i.project_id = td.project_id
JOIN agent_task_queue atq ON atq.issue_id = i.id AND atq.agent_id = td.train_agent_id
WHERE td.project_id = $1
ORDER BY atq.created_at DESC
LIMIT 1;
