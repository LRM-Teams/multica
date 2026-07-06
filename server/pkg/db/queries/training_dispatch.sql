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
