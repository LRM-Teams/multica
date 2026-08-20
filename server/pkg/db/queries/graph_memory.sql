-- name: GetGraphMemoryProfile :one
SELECT workspace_id, memory_type, explore_agents, explore_max_rounds, updated_at,
       scoped_writer_ready, timezone,
       ttt_enabled, explore_nodes_per_expansion,
       max_hierarchy_fanout, max_relation_edges_per_node,
       dive_max_rounds, dive_max_viewed_nodes, dive_max_source_files,
       dive_timeout_seconds, w_round,
       source_max_file_bytes, source_max_total_bytes, source_max_pdf_pages,
       source_max_av_seconds, source_max_image_megapixels,
       dive_model, dive_provider, config_version, schema_version
FROM graph_memory_profile
WHERE workspace_id = $1;

-- name: CreateGraphMemoryProfile :one
INSERT INTO graph_memory_profile (
  workspace_id, memory_type, explore_agents, explore_max_rounds,
  ttt_enabled, explore_nodes_per_expansion,
  max_hierarchy_fanout, max_relation_edges_per_node,
  dive_max_rounds, dive_max_viewed_nodes, dive_max_source_files,
  dive_timeout_seconds, w_round,
  source_max_file_bytes, source_max_total_bytes, source_max_pdf_pages,
  source_max_av_seconds, source_max_image_megapixels,
  dive_model, dive_provider
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
RETURNING workspace_id, memory_type, explore_agents, explore_max_rounds, updated_at,
       scoped_writer_ready, timezone,
       ttt_enabled, explore_nodes_per_expansion,
       max_hierarchy_fanout, max_relation_edges_per_node,
       dive_max_rounds, dive_max_viewed_nodes, dive_max_source_files,
       dive_timeout_seconds, w_round,
       source_max_file_bytes, source_max_total_bytes, source_max_pdf_pages,
       source_max_av_seconds, source_max_image_megapixels,
       dive_model, dive_provider, config_version, schema_version;

-- name: UpdateGraphMemoryProfileCAS :one
UPDATE graph_memory_profile SET
  memory_type = $3,
  explore_agents = $4,
  explore_max_rounds = $5,
  ttt_enabled = $6,
  explore_nodes_per_expansion = $7,
  max_hierarchy_fanout = $8,
  max_relation_edges_per_node = $9,
  dive_max_rounds = $10,
  dive_max_viewed_nodes = $11,
  dive_max_source_files = $12,
  dive_timeout_seconds = $13,
  w_round = $14,
  source_max_file_bytes = $15,
  source_max_total_bytes = $16,
  source_max_pdf_pages = $17,
  source_max_av_seconds = $18,
  source_max_image_megapixels = $19,
  dive_model = $20,
  dive_provider = $21,
  config_version = config_version + 1,
  updated_at = now()
WHERE workspace_id = $1 AND config_version = $2
RETURNING workspace_id, memory_type, explore_agents, explore_max_rounds, updated_at,
       scoped_writer_ready, timezone,
       ttt_enabled, explore_nodes_per_expansion,
       max_hierarchy_fanout, max_relation_edges_per_node,
       dive_max_rounds, dive_max_viewed_nodes, dive_max_source_files,
       dive_timeout_seconds, w_round,
       source_max_file_bytes, source_max_total_bytes, source_max_pdf_pages,
       source_max_av_seconds, source_max_image_megapixels,
       dive_model, dive_provider, config_version, schema_version;

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
