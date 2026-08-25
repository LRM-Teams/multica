-- name: GetGraphMemoryProfile :one
SELECT *
FROM graph_memory_profile
WHERE workspace_id = $1;

-- name: CreateGraphMemoryProfile :one
INSERT INTO graph_memory_profile (
  workspace_id, memory_type, explore_agents, explore_max_rounds,
  ttt_enabled, recall_ttt_enabled, consolidation_ttt_enabled,
  graph_memory_mode, memory_agent_runtime_id, memory_agent_model, memory_agent_thinking,
  memory_agent_idle_grace_seconds, memory_agent_max_nodes_per_call,
  memory_agent_max_nodes_per_minute, memory_agent_max_continuous_turn_seconds,
  memory_agent_max_tokens_per_hour,
  explore_nodes_per_expansion, max_hierarchy_fanout, max_relation_edges_per_node,
  dive_max_rounds, dive_max_viewed_nodes, dive_max_source_files,
  dive_timeout_seconds, w_round,
  source_max_file_bytes, source_max_total_bytes, source_max_pdf_pages,
  source_max_av_seconds, source_max_image_megapixels,
  dive_model, dive_provider
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31)
RETURNING *;

-- name: UpdateGraphMemoryProfileCAS :one
UPDATE graph_memory_profile SET
  memory_type = $3,
  explore_agents = $4,
  explore_max_rounds = $5,
  ttt_enabled = $6,
  recall_ttt_enabled = $7,
  consolidation_ttt_enabled = $8,
  graph_memory_mode = $9,
  memory_agent_runtime_id = $10,
  memory_agent_model = $11,
  memory_agent_thinking = $12,
  memory_agent_idle_grace_seconds = $13,
  memory_agent_max_nodes_per_call = $14,
  memory_agent_max_nodes_per_minute = $15,
  memory_agent_max_continuous_turn_seconds = $16,
  memory_agent_max_tokens_per_hour = $17,
  explore_nodes_per_expansion = $18,
  max_hierarchy_fanout = $19,
  max_relation_edges_per_node = $20,
  dive_max_rounds = $21,
  dive_max_viewed_nodes = $22,
  dive_max_source_files = $23,
  dive_timeout_seconds = $24,
  w_round = $25,
  source_max_file_bytes = $26,
  source_max_total_bytes = $27,
  source_max_pdf_pages = $28,
  source_max_av_seconds = $29,
  source_max_image_megapixels = $30,
  dive_model = $31,
  dive_provider = $32,
  config_version = config_version + 1,
  updated_at = now()
WHERE workspace_id = $1 AND config_version = $2
RETURNING *;

-- name: GetGraphMemoryChannelBindingForUpdate :one
SELECT project_id FROM channel
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: GetGraphMemoryChannelRoute :one
SELECT workspace_id, channel_id, routing_mode, current_graph_kind, current_graph_owner_id, generation
FROM graph_memory_channel_route
WHERE channel_id = $1 AND workspace_id = $2;

-- name: GetGraphMemoryChannelRouteForUpdate :one
SELECT workspace_id, channel_id, routing_mode, current_graph_kind, current_graph_owner_id, generation
FROM graph_memory_channel_route
WHERE channel_id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: UpsertGraphMemoryChannelRoute :exec
INSERT INTO graph_memory_channel_route
  (workspace_id, channel_id, routing_mode, current_graph_kind, current_graph_owner_id, generation)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (channel_id) DO UPDATE SET
  routing_mode = EXCLUDED.routing_mode,
  current_graph_kind = EXCLUDED.current_graph_kind,
  current_graph_owner_id = EXCLUDED.current_graph_owner_id,
  generation = EXCLUDED.generation,
  updated_at = now();

-- name: CloseGraphMemoryChannelLineage :exec
UPDATE graph_memory_channel_lineage SET valid_to = now()
WHERE channel_id = $1 AND generation = $2 AND valid_to IS NULL;

-- name: AppendGraphMemoryChannelLineage :exec
INSERT INTO graph_memory_channel_lineage
  (workspace_id, channel_id, generation, graph_kind, graph_owner_id)
VALUES ($1, $2, $3, $4, $5);

-- name: ListGraphMemoryChannelLineage :many
SELECT workspace_id, channel_id, generation, graph_kind, graph_owner_id, valid_from, valid_to
FROM graph_memory_channel_lineage
WHERE workspace_id = $1 AND channel_id = $2
ORDER BY generation;

-- name: GetGraphMemoryScopedGate :one
SELECT memory_type, scoped_writer_ready, timezone FROM graph_memory_profile
WHERE workspace_id = $1;

-- name: InsertGraphMemoryConsolidationRun :one
INSERT INTO graph_memory_consolidation_run (workspace_id, trigger_kind)
VALUES ($1, $2)
RETURNING id, workspace_id, status, trigger_kind, error, details, created_at, started_at, finished_at;

-- name: FinishGraphMemoryConsolidationRun :exec
UPDATE graph_memory_consolidation_run
SET status = $2, error = $3, details = $4, started_at = COALESCE(started_at, now()), finished_at = now()
WHERE id = $1;

-- name: GetGraphMemoryConsolidationRun :one
SELECT id, workspace_id, status, trigger_kind, error, details, created_at, started_at, finished_at
FROM graph_memory_consolidation_run
WHERE id = $1 AND workspace_id = $2;

-- name: ListGraphMemoryConsolidationRuns :many
SELECT id, workspace_id, status, trigger_kind, error, details, created_at, started_at, finished_at
FROM graph_memory_consolidation_run
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT 20;
