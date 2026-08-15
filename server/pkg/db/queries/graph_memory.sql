-- name: GetGraphMemoryProfile :one
SELECT workspace_id, memory_type, explore_agents, explore_max_rounds, updated_at
FROM graph_memory_profile
WHERE workspace_id = $1;

-- name: UpsertGraphMemoryProfile :one
INSERT INTO graph_memory_profile (workspace_id, memory_type, explore_agents, explore_max_rounds)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id) DO UPDATE SET
  memory_type = EXCLUDED.memory_type,
  explore_agents = EXCLUDED.explore_agents,
  explore_max_rounds = EXCLUDED.explore_max_rounds,
  updated_at = now()
RETURNING workspace_id, memory_type, explore_agents, explore_max_rounds, updated_at;
