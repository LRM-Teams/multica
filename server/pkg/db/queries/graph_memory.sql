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

-- name: InsertGraphMemoryAtom :execrows
-- One atom of the publish transaction (Task 7). The write-once trigger in
-- migration 466 keeps accepted atoms immutable; re-publish attempts converge
-- through ON CONFLICT DO NOTHING because atom identity is content-addressed.
INSERT INTO graph_memory_atom (
  workspace_id, atom_id, segment_id, body, kind, source_message_seqs,
  source_tool, tool_trust_class, content_hash, artifact_ref,
  visibility, channel_id, project_id, publish_seq
) VALUES (
  sqlc.arg(workspace_id), sqlc.arg(atom_id), sqlc.arg(segment_id),
  sqlc.arg(body), sqlc.arg(kind), sqlc.arg(source_message_seqs)::integer[],
  sqlc.arg(source_tool), sqlc.arg(tool_trust_class), sqlc.arg(content_hash),
  sqlc.narg(artifact_ref), sqlc.arg(visibility), sqlc.narg(channel_id),
  sqlc.narg(project_id), sqlc.arg(publish_seq)
) ON CONFLICT (workspace_id, atom_id) DO NOTHING;

-- name: EnqueueGraphMemoryProjection :execrows
-- The durable graph projection request, written in the same publish
-- transaction as the atoms it covers (Task 8 consumes it; nothing scans).
INSERT INTO graph_memory_projection_outbox (
  workspace_id, segment_id, request_hash, route_generation
) VALUES (
  sqlc.arg(workspace_id), sqlc.arg(segment_id), sqlc.arg(request_hash),
  sqlc.narg(route_generation)
) ON CONFLICT (workspace_id, segment_id) DO NOTHING;

-- name: ListGraphMemoryAtomsBySegment :many
SELECT atom_id, segment_id, body, kind, source_message_seqs, source_tool,
       tool_trust_class, content_hash, artifact_ref, visibility,
       channel_id, project_id, publish_seq, created_at
FROM graph_memory_atom
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
ORDER BY atom_id;

-- name: ClaimGraphMemoryProjectionOutbox :many
-- Leases claimable graph projection requests: pending rows, retry rows whose
-- backoff elapsed, and processing rows whose lease expired. Concurrent
-- projectors claim disjoint sets. This outbox is the ONLY work-discovery
-- surface for graph projection (Task 8): nothing scans segments or atoms.
WITH claimable AS (
  SELECT workspace_id, segment_id
  FROM graph_memory_projection_outbox
  WHERE status = 'pending'
     OR (status = 'retry' AND next_attempt_at <= now())
     OR (status = 'processing' AND lease_expires_at < now())
  ORDER BY updated_at, segment_id
  LIMIT sqlc.arg(max_rows)
  FOR UPDATE SKIP LOCKED
), leased AS (
  UPDATE graph_memory_projection_outbox AS outbox
  SET status = 'processing',
      lease_owner = sqlc.arg(lease_owner),
      lease_expires_at = sqlc.arg(lease_expires_at),
      next_attempt_at = NULL,
      updated_at = now()
  FROM claimable
  WHERE outbox.workspace_id = claimable.workspace_id
    AND outbox.segment_id = claimable.segment_id
  RETURNING outbox.workspace_id, outbox.segment_id, outbox.request_hash,
            outbox.attempts, outbox.route_generation
)
SELECT leased.workspace_id, leased.segment_id, leased.request_hash,
       leased.attempts, leased.route_generation
FROM leased
ORDER BY leased.segment_id;

-- name: GetGraphMemoryProjectionForUpdate :one
SELECT workspace_id, segment_id, request_hash, route_generation, status,
       attempts, lease_owner, lease_expires_at, next_attempt_at, last_error,
       created_at, updated_at, completed_at
FROM graph_memory_projection_outbox
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
FOR UPDATE;

-- name: CompleteGraphMemoryProjection :execrows
UPDATE graph_memory_projection_outbox
SET status = 'completed',
    lease_owner = NULL,
    lease_expires_at = NULL,
    next_attempt_at = NULL,
    last_error = NULL,
    completed_at = now(),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND status = 'processing';

-- name: RetryGraphMemoryProjection :execrows
-- CAS-guarded retry bookkeeping for a failed projection attempt.
UPDATE graph_memory_projection_outbox
SET status = 'retry',
    attempts = sqlc.arg(attempts),
    lease_owner = NULL,
    lease_expires_at = NULL,
    next_attempt_at = sqlc.arg(next_attempt_at),
    last_error = sqlc.arg(last_error),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND status = 'processing'
  AND attempts = sqlc.arg(current_attempts);

-- name: FailGraphMemoryProjection :execrows
UPDATE graph_memory_projection_outbox
SET status = sqlc.arg(terminal_status),
    attempts = GREATEST(attempts, sqlc.arg(attempts)),
    lease_owner = NULL,
    lease_expires_at = NULL,
    next_attempt_at = NULL,
    last_error = sqlc.arg(last_error),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND status = 'processing';

-- name: GetGraphMemoryLineageAtGeneration :one
-- Event-time route identity: the lineage row a channel segment was frozen
-- with, whether or later route transitions have since closed it.
SELECT workspace_id, channel_id, generation, graph_kind, graph_owner_id,
       valid_from, valid_to
FROM graph_memory_channel_lineage
WHERE workspace_id = sqlc.arg(workspace_id)
  AND channel_id = sqlc.arg(channel_id)
  AND generation = sqlc.arg(generation);

-- name: UpsertMemorySourceGuard :exec
-- Future publishes upsert their source's guard row so retraction can always
-- fence it (Task 8A backfill covers pre-467 sources).
INSERT INTO memory_source_guard (workspace_id, source_kind, source_id)
VALUES (sqlc.arg(workspace_id), sqlc.arg(source_kind), sqlc.arg(source_id))
ON CONFLICT (workspace_id, source_kind, source_id) DO NOTHING;

-- name: LockMemorySourceGuardsForUpdate :many
-- Deterministic sorted order: every retraction locks the same key order, so
-- concurrent retractions of overlapping source sets cannot deadlock. Source
-- keys are "kind:id" pairs (kind is a closed lowercase slug, id a uuid).
SELECT workspace_id, source_kind, source_id, retracted_at, retracted_by, reason
FROM memory_source_guard
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (source_kind || ':' || source_id) = ANY (sqlc.arg(source_keys)::text[])
ORDER BY source_kind, source_id
FOR UPDATE;

-- name: FenceMemorySourceGuard :execrows
UPDATE memory_source_guard
SET retracted_at = now(),
    retracted_by = sqlc.arg(retracted_by),
    reason = sqlc.arg(reason),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (source_kind || ':' || source_id) = ANY (sqlc.arg(source_keys)::text[])
  AND retracted_at IS NULL;

-- name: InsertRetractionRegistry :one
INSERT INTO retraction_registry (workspace_id, actor, reason, source_count)
VALUES (sqlc.arg(workspace_id), sqlc.arg(actor), sqlc.arg(reason), sqlc.arg(source_count))
RETURNING id, workspace_id, actor, reason, source_count, created_at;

-- name: ProvenanceConsumersForSources :many
-- The complete currently published reverse-provenance closure of the fenced
-- sources: every consumer that must be quarantined.
SELECT DISTINCT consumer_kind, consumer_id
FROM memory_source_provenance
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (source_kind || ':' || source_id) = ANY (sqlc.arg(source_keys)::text[]);

-- name: InsertQuarantinedPendingRecompute :execrows
-- Consumer ids may themselves contain ':' (migrated atom copies
-- "<atom>:mig<gen>", daily node ids), so only the FIRST colon separates
-- kind from id.
INSERT INTO quarantined_pending_recompute (workspace_id, retraction_id, consumer_kind, consumer_id)
SELECT sqlc.arg(workspace_id), sqlc.arg(retraction_id),
       split_part(key, ':', 1), substring(key from strpos(key, ':') + 1)
FROM unnest(sqlc.arg(consumer_keys)::text[]) AS t(key)
ON CONFLICT DO NOTHING;

-- name: InsertMemoryDeletionAudit :execrows
INSERT INTO memory_deletion_audit (workspace_id, retraction_id, source_kind, source_id, quarantined_count)
SELECT sqlc.arg(workspace_id), sqlc.arg(retraction_id),
       split_part(key, ':', 1), split_part(key, ':', 2), sqlc.arg(quarantined_count)
FROM unnest(sqlc.arg(source_keys)::text[]) AS t(key);

-- name: RetractedMemorySources :many
-- Read-gate check: which of the requested refs are fenced.
SELECT source_kind, source_id
FROM memory_source_guard
WHERE workspace_id = sqlc.arg(workspace_id)
  AND retracted_at IS NOT NULL
  AND (source_kind || ':' || source_id) = ANY (sqlc.arg(source_keys)::text[]);

-- name: GetMemoryReadPhaseGate :one
-- Absent row = every route disabled (DB default off).
SELECT workspace_id, atoms_enabled, search_v2_enabled, explore_enabled,
       citations_enabled, atom_consolidation_enabled, retraction_canary_ok, updated_at
FROM memory_read_phase_gate
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: SetMemoryReadPhaseGate :execrows
-- The only sanctioned way to flip a route on; requires the retraction canary.
UPDATE memory_read_phase_gate
SET atoms_enabled = sqlc.arg(atoms_enabled),
    search_v2_enabled = sqlc.arg(search_v2_enabled),
    explore_enabled = sqlc.arg(explore_enabled),
    citations_enabled = sqlc.arg(citations_enabled),
    atom_consolidation_enabled = sqlc.arg(atom_consolidation_enabled),
    retraction_canary_ok = sqlc.arg(retraction_canary_ok),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertMemorySourceGuards :exec
-- RetractSourcesTx registers the sources it is about to fence: a business
-- delete of a source that never published anything still fences it.
INSERT INTO memory_source_guard (workspace_id, source_kind, source_id)
SELECT sqlc.arg(workspace_id), split_part(key, ':', 1), split_part(key, ':', 2)
FROM unnest(sqlc.arg(source_keys)::text[]) AS t(key)
ON CONFLICT DO NOTHING;

-- name: RegisterWorkspaceMemorySourceGuard :exec
-- The workspace's own canonical source row, registered before the set-based
-- workspace fence so a workspace that never published still fences.
INSERT INTO memory_source_guard (workspace_id, source_kind, source_id)
VALUES (sqlc.arg(workspace_id), 'workspace', sqlc.arg(workspace_id_text))
ON CONFLICT (workspace_id, source_kind, source_id) DO NOTHING;

-- name: FenceWorkspaceMemorySourceGuards :execrows
-- Set-based fence of every canonical source in the workspace (Task 8A
-- workspace bulk delete). The UPDATE takes the row locks itself; rows
-- already retracted keep their original attribution.
UPDATE memory_source_guard
SET retracted_at = now(),
    retracted_by = sqlc.arg(retracted_by),
    reason = sqlc.arg(reason),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND retracted_at IS NULL;

-- name: QuarantineWorkspaceProvenance :execrows
-- Complete quarantined reverse-provenance closure of every source the
-- workspace fence covered.
INSERT INTO quarantined_pending_recompute (workspace_id, retraction_id, consumer_kind, consumer_id)
SELECT p.workspace_id, sqlc.arg(retraction_id), p.consumer_kind, p.consumer_id
FROM memory_source_provenance p
WHERE p.workspace_id = sqlc.arg(workspace_id)
ON CONFLICT DO NOTHING;

-- name: InsertWorkspaceDeletionAudit :exec
-- One aggregate audit row for the set-based workspace fence.
INSERT INTO memory_deletion_audit (workspace_id, retraction_id, source_kind, source_id, quarantined_count)
VALUES (sqlc.arg(workspace_id), sqlc.arg(retraction_id), 'workspace', sqlc.arg(workspace_id_text), sqlc.arg(quarantined_count));

-- name: UpsertMemoryExplorePlan :one
-- Persists one Explore plan (Task 10). A replayed start for the same
-- trajectory keeps the high-water marks (GREATEST) and counts one rollover;
-- the caller reads the route gate before calling, so a disabled route never
-- reaches this statement.
INSERT INTO memory_explore_plan (
    workspace_id, trajectory_id, pinned_graphs,
    segment_publish_seq_max, interaction_edge_seq_max, budgets
) VALUES (
    sqlc.arg(workspace_id), sqlc.arg(trajectory_id), sqlc.arg(pinned_graphs)::jsonb,
    sqlc.arg(segment_publish_seq_max), sqlc.arg(interaction_edge_seq_max),
    sqlc.arg(budgets)::jsonb
)
ON CONFLICT (workspace_id, trajectory_id) DO UPDATE SET
    pinned_graphs = EXCLUDED.pinned_graphs,
    segment_publish_seq_max = GREATEST(
        memory_explore_plan.segment_publish_seq_max, EXCLUDED.segment_publish_seq_max),
    interaction_edge_seq_max = GREATEST(
        memory_explore_plan.interaction_edge_seq_max, EXCLUDED.interaction_edge_seq_max),
    budgets = EXCLUDED.budgets,
    rollover_count = memory_explore_plan.rollover_count + 1,
    updated_at = now()
RETURNING workspace_id, trajectory_id, pinned_graphs, segment_publish_seq_max,
          interaction_edge_seq_max, budgets, rollover_count, created_at, updated_at;

-- name: GetMemoryExplorePlan :one
SELECT workspace_id, trajectory_id, pinned_graphs, segment_publish_seq_max,
       interaction_edge_seq_max, budgets, rollover_count, created_at, updated_at
FROM memory_explore_plan
WHERE workspace_id = sqlc.arg(workspace_id)
  AND trajectory_id = sqlc.arg(trajectory_id);

-- name: MemoryExploreWatermarks :one
-- The frozen read ceilings of one workspace: the highest published segment
-- watermark and the highest interaction edge sequence.
SELECT
    (SELECT COALESCE(MAX(seg.publish_seq), 0)::bigint FROM interaction_dag_segment seg
      WHERE seg.workspace_id = sqlc.arg(workspace_id)) AS segment_publish_seq_max,
    (SELECT COALESCE(MAX(edge.edge_seq), 0)::bigint FROM interaction_dag_edge edge
      WHERE edge.workspace_id = sqlc.arg(workspace_id)) AS interaction_edge_seq_max;

-- name: ResolveStagingAtomForRef :one
-- Staging-atom resolution (Task 10 Step 2): atom -> owning segment -> its
-- canonical task_output source, so the resolver can recheck the Task 8A
-- retraction registry on every resolve.
SELECT atom.atom_id,
       atom.segment_id,
       segment.agent_run_id::text AS source_id,
       COALESCE(segment.channel_id_at_event::text, '')::text AS channel_id_at_event,
       COALESCE(segment.project_id_at_event::text, '')::text AS project_id_at_event
FROM graph_memory_atom atom
JOIN interaction_dag_segment segment
  ON segment.workspace_id = atom.workspace_id
 AND segment.segment_id = atom.segment_id
WHERE atom.workspace_id = sqlc.arg(workspace_id)
  AND atom.atom_id = sqlc.arg(atom_id);

-- name: InsertMemoryReadPhaseGate :execrows
-- Registers the default-all-off gate row (operator transitions happen via
-- SetMemoryReadPhaseGate).
INSERT INTO memory_read_phase_gate (workspace_id)
VALUES (sqlc.arg(workspace_id))
ON CONFLICT (workspace_id) DO NOTHING;

-- name: ListStagingAtomsAtWatermark :many
-- Explore v2 seed channel (Task 11): active atoms of the pinned scopes,
-- readable only up to the plan's frozen publish watermark.
SELECT atom.atom_id, atom.segment_id,
       COALESCE(atom.channel_id::text, '')::text AS channel_id,
       COALESCE(atom.project_id::text, '')::text AS project_id,
       atom.publish_seq::bigint
FROM graph_memory_atom atom
WHERE atom.workspace_id = sqlc.arg(workspace_id)
  AND atom.publish_seq <= sqlc.arg(publish_seq_max)
  AND (
        (sqlc.arg(channel_id)::text <> '' AND atom.channel_id::text = sqlc.arg(channel_id))
     OR (sqlc.arg(channel_id)::text = '' AND atom.channel_id IS NULL AND atom.project_id IS NOT NULL)
  )
ORDER BY atom.publish_seq DESC, atom.atom_id
LIMIT sqlc.arg(limit_rows);

-- name: ListSiblingAtoms :many
-- Sibling atoms of one segment (same-segment neighborhood, Task 11).
SELECT atom_id
FROM graph_memory_atom
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND atom_id <> sqlc.arg(except_atom_id)
ORDER BY atom_id
LIMIT sqlc.arg(limit_rows);

-- name: ListDAGEdgesAroundSegment :many
-- Bidirectional interaction-DAG neighborhood of one segment, readable only
-- up to the plan's frozen edge watermark (Task 11).
SELECT edge.edge_seq, edge.type, edge.src_segment_id, edge.dst_segment_id
FROM interaction_dag_edge edge
WHERE edge.workspace_id = sqlc.arg(workspace_id)
  AND edge.edge_seq <= sqlc.arg(edge_seq_max)
  AND (edge.src_segment_id = sqlc.arg(segment_id) OR edge.dst_segment_id = sqlc.arg(segment_id))
ORDER BY edge.edge_seq
LIMIT sqlc.arg(limit_rows);

-- name: GetSegmentEvidence :one
-- Evidence source of one segment (Task 11 Step 4): the summary-first fields
-- and the sanitized trajectory payload, at the segment's frozen publish
-- watermark. The source fence is rechecked by the caller.
SELECT segment_id, closing_event, trajectory, publish_seq, content_status
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id);

-- name: ListSegmentAtomBodies :many
-- Summary fallback for canonical segments (Task 11 Step 4): their atoms are
-- the summary until a reviewer writes one.
SELECT atom_id, body
FROM graph_memory_atom
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
ORDER BY atom_id
LIMIT sqlc.arg(limit_rows);

-- name: ListActiveAtomSnapshot :many
-- Task 13 adoption loader: the active atom ledger of one workspace scope at
-- a publish watermark. Retracted atoms are excluded by the Task 8A
-- quarantine closure; the retriever re-asserts the exclusions it can prove
-- locally after InstallAtomSnapshot.
SELECT atom.atom_id, atom.segment_id, atom.body,
       COALESCE(atom.channel_id::text, '')::text AS channel_id,
       atom.publish_seq::bigint, atom.created_at
FROM graph_memory_atom atom
WHERE atom.workspace_id = sqlc.arg(workspace_id)
  AND atom.publish_seq <= sqlc.arg(publish_seq_max)
  AND (
        (sqlc.arg(scope_channel_id)::text <> '' AND atom.channel_id::text = sqlc.arg(scope_channel_id))
     OR (sqlc.arg(scope_channel_id)::text = '' AND atom.channel_id IS NULL AND atom.project_id IS NOT NULL)
  )
  AND NOT EXISTS (
        SELECT 1 FROM quarantined_pending_recompute q
        WHERE q.workspace_id = atom.workspace_id
          AND q.consumer_kind = 'graph_memory_atom' AND q.consumer_id = atom.atom_id
  )
ORDER BY atom.publish_seq DESC, atom.atom_id
LIMIT sqlc.arg(limit_rows);

-- name: ListQuarantinedAtomIDs :many
-- Retracted atom ids of one workspace (the re-assertion set handed to
-- InstallAtomSnapshot alongside the active snapshot).
SELECT consumer_id
FROM quarantined_pending_recompute
WHERE workspace_id = sqlc.arg(workspace_id)
  AND consumer_kind = 'graph_memory_atom';

-- name: MaxAtomPublishSeq :one
-- The workspace's current atom publish watermark (0 when no atom exists).
SELECT COALESCE(max(publish_seq), 0)::bigint
FROM graph_memory_atom
WHERE workspace_id = sqlc.arg(workspace_id);

-- ============ Task 14: DB-authoritative publication ledger ============

-- name: LockMemorySourceGuardsKeyShare :many
-- Publication lock: every contributing source is held FOR KEY SHARE in the
-- same deterministic source-key order the deletion path uses for FOR UPDATE,
-- so delete-first aborts the publication and publish-first makes the
-- deletion wait for the transaction that will quarantine the new closure.
SELECT workspace_id, source_kind, source_id, retracted_at
FROM memory_source_guard
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (source_kind || ':' || source_id) = ANY (sqlc.arg(source_keys)::text[])
ORDER BY source_kind, source_id
FOR KEY SHARE;

-- name: GetGraphMemoryPublication :one
SELECT workspace_id, graph_kind, graph_owner_id, current_generation,
       graph_version, file_manifest_hash, published_at, published_by
FROM graph_memory_publication
WHERE workspace_id = sqlc.arg(workspace_id)
  AND graph_kind = sqlc.arg(graph_kind)
  AND graph_owner_id = sqlc.arg(graph_owner_id);

-- name: CASPublishGraphMemoryGeneration :execrows
-- The publication CAS: generation advances only while the row still holds
-- the base generation the candidate planned against. Zero rows affected
-- means a concurrent publication won and this candidate is stale.
INSERT INTO graph_memory_publication (
    workspace_id, graph_kind, graph_owner_id, current_generation,
    graph_version, file_manifest_hash, published_by
) VALUES (
    sqlc.arg(workspace_id), sqlc.arg(graph_kind), sqlc.arg(graph_owner_id),
    sqlc.arg(base_generation) + 1,
    sqlc.arg(graph_version), sqlc.arg(file_manifest_hash), sqlc.arg(published_by)
) ON CONFLICT (workspace_id, graph_kind, graph_owner_id) DO UPDATE
SET current_generation = sqlc.arg(base_generation) + 1,
    graph_version = sqlc.arg(graph_version),
    file_manifest_hash = sqlc.arg(file_manifest_hash),
    published_at = now(),
    published_by = sqlc.arg(published_by)
WHERE graph_memory_publication.current_generation = sqlc.arg(base_generation);

-- name: UpsertGraphMemoryPublicationIndex :exec
-- Reader-facing active pointer; written in the same transaction as the CAS
-- so an observer of the row observes the complete generation.
INSERT INTO graph_memory_publication_index (
    workspace_id, graph_kind, graph_owner_id, active_generation,
    graph_version, file_manifest_hash
) VALUES (
    sqlc.arg(workspace_id), sqlc.arg(graph_kind), sqlc.arg(graph_owner_id),
    sqlc.arg(active_generation), sqlc.arg(graph_version), sqlc.arg(file_manifest_hash)
) ON CONFLICT (workspace_id, graph_kind, graph_owner_id) DO UPDATE
SET active_generation = EXCLUDED.active_generation,
    graph_version = EXCLUDED.graph_version,
    file_manifest_hash = EXCLUDED.file_manifest_hash,
    activated_at = now();

-- name: GetGraphMemoryPublicationIndex :one
-- Reader authority. Absent row = the scope predates publication; readers
-- fall back to the file-store current pointer (recoverable projection).
SELECT workspace_id, graph_kind, graph_owner_id, active_generation,
       graph_version, file_manifest_hash, activated_at
FROM graph_memory_publication_index
WHERE workspace_id = sqlc.arg(workspace_id)
  AND graph_kind = sqlc.arg(graph_kind)
  AND graph_owner_id = sqlc.arg(graph_owner_id);

-- name: InsertGraphMemoryPublicationCoverage :execrows
-- The exact atom closure this generation consumed; a later retraction joins
-- against it to quarantine precisely the affected published nodes.
INSERT INTO graph_memory_publication_coverage (
    workspace_id, graph_kind, graph_owner_id, generation, atom_id, segment_id
)
SELECT sqlc.arg(workspace_id)::uuid, sqlc.arg(graph_kind), sqlc.arg(graph_owner_id)::uuid,
       sqlc.arg(generation), coverage.atom_id, coverage.segment_id
FROM (
  SELECT unnest(sqlc.arg(atom_ids)::text[]) AS atom_id,
         unnest(sqlc.arg(segment_ids)::text[]) AS segment_id
) AS coverage
ON CONFLICT DO NOTHING;

-- name: InsertGraphMemoryPublicationProvenanceRow :exec
-- Reverse provenance: one published node and the atoms/segments it cites.
INSERT INTO graph_memory_publication_provenance (
    workspace_id, graph_kind, graph_owner_id, generation, node_id, atom_ids, segment_ids
) VALUES (
    sqlc.arg(workspace_id), sqlc.arg(graph_kind), sqlc.arg(graph_owner_id),
    sqlc.arg(generation), sqlc.arg(node_id),
    sqlc.arg(atom_ids)::text[], sqlc.arg(segment_ids)::text[]
)
ON CONFLICT (workspace_id, graph_kind, graph_owner_id, generation, node_id) DO UPDATE
SET atom_ids = EXCLUDED.atom_ids, segment_ids = EXCLUDED.segment_ids;

-- name: InsertGraphMemoryPublicationOutcome :exec
-- What happened to one publication attempt. Aggregate counters only —
-- never node bodies or payloads.
INSERT INTO graph_memory_publication_outcome (
    workspace_id, graph_kind, graph_owner_id, generation, outcome,
    graph_version, file_manifest_hash, covered_atom_count,
    covered_segment_count, node_count, source_keys
) VALUES (
    sqlc.arg(workspace_id), sqlc.arg(graph_kind), sqlc.arg(graph_owner_id),
    sqlc.arg(generation), sqlc.arg(outcome), sqlc.arg(graph_version),
    sqlc.arg(file_manifest_hash), sqlc.arg(covered_atom_count),
    sqlc.arg(covered_segment_count), sqlc.arg(node_count),
    sqlc.arg(source_keys)::text[]
)
ON CONFLICT (workspace_id, graph_kind, graph_owner_id, generation) DO UPDATE
SET outcome = EXCLUDED.outcome, graph_version = EXCLUDED.graph_version,
    file_manifest_hash = EXCLUDED.file_manifest_hash,
    covered_atom_count = EXCLUDED.covered_atom_count,
    covered_segment_count = EXCLUDED.covered_segment_count,
    node_count = EXCLUDED.node_count, source_keys = EXCLUDED.source_keys;

-- name: PublishedNodesCoveringAtoms :many
-- Retraction join: the published nodes whose provenance cites any of the
-- retracted atoms, scoped to the ACTIVE generation of each graph.
SELECT prov.workspace_id, prov.graph_kind, prov.graph_owner_id,
       prov.generation, prov.node_id
FROM graph_memory_publication_provenance prov
JOIN graph_memory_publication_index idx
  ON idx.workspace_id = prov.workspace_id
 AND idx.graph_kind = prov.graph_kind
 AND idx.graph_owner_id = prov.graph_owner_id
 AND idx.active_generation = prov.generation
WHERE prov.workspace_id = sqlc.arg(workspace_id)::uuid
  AND prov.atom_ids && sqlc.arg(atom_ids)::text[];

-- name: CoverageAtomsForSegments :many
-- Active-generation coverage rows for the given segments, used to build the
-- atom manifest of a publication candidate from DB-authoritative state.
SELECT cov.atom_id, cov.segment_id
FROM graph_memory_publication_coverage cov
JOIN graph_memory_publication_index idx
  ON idx.workspace_id = cov.workspace_id
 AND idx.graph_kind = cov.graph_kind
 AND idx.graph_owner_id = cov.graph_owner_id
 AND idx.active_generation = cov.generation
WHERE cov.workspace_id = sqlc.arg(workspace_id)::uuid
  AND cov.segment_id = ANY (sqlc.arg(segment_ids)::text[]);

-- name: CountScopeUncoveredActiveAtoms :one
-- Task 14 scheduler trigger input: the scope's active atoms the active
-- publication generation has not covered yet, plus the scope's total
-- active atom count (the failure-backoff watermark). Staging files are
-- never replayed; a quarantined atom leaves both counts.
SELECT
    COUNT(*) FILTER (WHERE NOT EXISTS (
        SELECT 1
        FROM graph_memory_publication_coverage cov
        JOIN graph_memory_publication_index idx
          ON idx.workspace_id = cov.workspace_id
         AND idx.graph_kind = cov.graph_kind
         AND idx.graph_owner_id = cov.graph_owner_id
         AND idx.active_generation = cov.generation
        WHERE cov.workspace_id = atom.workspace_id
          AND cov.graph_kind = sqlc.arg(graph_kind)
          AND cov.graph_owner_id = sqlc.arg(graph_owner_id)
          AND cov.atom_id = atom.atom_id
    ))::bigint AS uncovered_count,
    COUNT(*)::bigint AS total_count
FROM graph_memory_atom atom
WHERE atom.workspace_id = sqlc.arg(workspace_id)
  AND (
        (sqlc.arg(scope_channel_id)::text <> '' AND atom.channel_id::text = sqlc.arg(scope_channel_id))
     OR (sqlc.arg(scope_channel_id)::text = '' AND atom.channel_id IS NULL AND atom.project_id IS NOT NULL)
  )
  AND NOT EXISTS (
        SELECT 1 FROM quarantined_pending_recompute q
        WHERE q.workspace_id = atom.workspace_id
          AND q.consumer_kind = 'graph_memory_atom' AND q.consumer_id = atom.atom_id
  );

-- ============ Task 15: durable-evidence Project promotion ============

-- name: GetGraphMemoryChannelBinding :one
-- The channel's current project binding (plain read; the locking variant is
-- reserved for route transitions).
SELECT project_id FROM channel WHERE id = $1 AND workspace_id = $2;

-- name: ListGraphMemoryAtomsByIDs :many
-- Promotion evidence resolution: the exact atoms an LLM proposal cites.
SELECT atom_id, segment_id, body, channel_id, project_id, tool_trust_class
FROM graph_memory_atom
WHERE workspace_id = sqlc.arg(workspace_id) AND atom_id = ANY(sqlc.arg(atom_ids)::text[]);

-- name: GetChannelMessageForPromotion :one
-- Human-confirmation evidence: the message, its author, and its channel.
SELECT author_type, author_id, channel_id FROM channel_message
WHERE id = $1 AND workspace_id = $2;

-- name: GetIssueForPromotion :one
-- Formal-decision evidence: a workspace issue's terminal status.
SELECT status FROM issue WHERE id = $1 AND workspace_id = $2;

-- name: CountPublishedSegmentsForTask :one
-- Completed non-rolled-back outcome evidence: the canonical task has at
-- least one published (not retracted/dead-lettered) DAG segment.
SELECT count(*) FROM interaction_dag_segment
WHERE workspace_id = $1 AND agent_run_id = $2 AND content_status = 'published';

-- name: ExistsWorkspaceMember :one
-- Event-time author permission ground: the author is a workspace member.
SELECT EXISTS (SELECT 1 FROM member WHERE workspace_id = $1 AND user_id = $2);

-- ---------------------------------------------------------------------------
-- Task 16: channel-owned Graph migration (spec §12). Binding generations,
-- the copy ledger, citation redirects, and blob refs.
-- ---------------------------------------------------------------------------

-- name: MaxGraphMemoryChannelBindingGeneration :one
SELECT COALESCE(MAX(generation), 0)::bigint AS max_generation
FROM graph_memory_channel_binding
WHERE channel_id = $1;

-- name: InsertGraphMemoryChannelBinding :one
-- Written in the same transaction as the channel.project_id UPDATE; the
-- txid column is what the binding guard trigger matches.
INSERT INTO graph_memory_channel_binding (
    workspace_id, channel_id, generation, old_project_id, new_project_id,
    route_kind, route_owner_id, route_generation, source_watermark, actor, txid
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    pg_current_xact_id()::text::bigint
) RETURNING id;

-- name: ListGraphMemoryChannelBindings :many
SELECT * FROM graph_memory_channel_binding
WHERE channel_id = $1
ORDER BY generation DESC
LIMIT $2;

-- name: GetGraphMemoryChannelBindingByGeneration :one
SELECT * FROM graph_memory_channel_binding
WHERE channel_id = $1 AND generation = $2;

-- name: GraphMemoryChannelAtomWatermark :one
-- High-water publish_seq of the channel's channel-owned atoms at binding
-- time: the worker copies at or below this watermark only.
SELECT COALESCE(MAX(publish_seq), 0)::bigint AS watermark
FROM graph_memory_atom
WHERE workspace_id = $1 AND channel_id = $2 AND visibility = 'channel';

-- name: InsertGraphMemoryChannelMigrationState :exec
INSERT INTO graph_memory_channel_migration_state (
    workspace_id, channel_id, binding_generation, phase, source_watermark
) VALUES ($1, $2, $3, 'pending', $4)
ON CONFLICT (channel_id, binding_generation) DO NOTHING;

-- name: GetGraphMemoryChannelMigrationState :one
SELECT * FROM graph_memory_channel_migration_state
WHERE channel_id = $1 AND binding_generation = $2;

-- name: GetGraphMemoryChannelMigrationStateForUpdate :one
SELECT * FROM graph_memory_channel_migration_state
WHERE channel_id = $1 AND binding_generation = $2
FOR UPDATE;

-- name: ListGraphMemoryChannelMigrationsByPhase :many
SELECT * FROM graph_memory_channel_migration_state
WHERE phase = ANY(sqlc.arg(phases)::text[])
ORDER BY created_at
LIMIT sqlc.arg(limit_count);

-- name: ClaimGraphMemoryChannelMigration :exec
-- Worker claim: pending -> copying (idempotent replay finishes a stuck
-- copying row instead of duplicating it).
UPDATE graph_memory_channel_migration_state
SET phase = 'copying', updated_at = now()
WHERE channel_id = $1 AND binding_generation = $2
  AND phase IN ('pending', 'copying');

-- name: FinishGraphMemoryChannelMigration :exec
UPDATE graph_memory_channel_migration_state
SET phase = 'completed', copied_atoms = $3, copied_nodes = $4, copied_edges = $5,
    error = '', updated_at = now()
WHERE channel_id = $1 AND binding_generation = $2;

-- name: AbortGraphMemoryChannelMigration :exec
UPDATE graph_memory_channel_migration_state
SET phase = 'aborted', error = $3, updated_at = now()
WHERE channel_id = $1 AND binding_generation = $2;

-- name: UpsertGraphMemoryMigrationRedirect :exec
INSERT INTO graph_memory_migration_redirect (
    workspace_id, old_kind, old_id, new_kind, new_id, binding_generation
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (workspace_id, old_kind, old_id) DO NOTHING;

-- name: GetGraphMemoryMigrationRedirect :one
SELECT new_kind, new_id FROM graph_memory_migration_redirect
WHERE workspace_id = $1 AND old_kind = $2 AND old_id = $3;

-- name: ListGraphMemoryMigrationRedirectsByNewID :many
-- Reverse resolution: deletion through NEW canonical refs must reach the
-- OLD copies' consumers too.
SELECT old_kind, old_id FROM graph_memory_migration_redirect
WHERE workspace_id = $1 AND new_kind = $2
  AND new_id = ANY(sqlc.arg(new_ids)::text[]);

-- name: ListGraphMemoryMigrationRedirectOldIDs :many
-- The old refs that already have copies (replay filter + tombstone set).
SELECT old_id FROM graph_memory_migration_redirect
WHERE workspace_id = $1 AND old_kind = $2
  AND old_id = ANY(sqlc.arg(old_ids)::text[]);

-- name: InsertGraphMemoryMigrationBlobRef :exec
INSERT INTO graph_memory_migration_blob_ref (
    workspace_id, channel_id, binding_generation, blob_ref
) VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: ListGraphMemoryChannelAtomsForMigration :many
-- Channel-owned atoms at or below the watermark that have not been copied
-- yet (redirect existence = already copied).
SELECT a.atom_id, a.segment_id, a.body, a.kind, a.source_message_seqs, a.source_tool,
       a.tool_trust_class, a.content_hash, a.artifact_ref, a.publish_seq, a.created_at
FROM graph_memory_atom a
WHERE a.workspace_id = $1 AND a.channel_id = $2 AND a.visibility = 'channel'
  AND a.publish_seq <= $3
  AND NOT EXISTS (
      SELECT 1 FROM graph_memory_migration_redirect r
      WHERE r.workspace_id = a.workspace_id
        AND r.old_kind = 'atom' AND r.old_id = a.atom_id)
ORDER BY a.publish_seq
LIMIT $4;

-- name: InsertMigratedGraphMemoryAtom :exec
INSERT INTO graph_memory_atom (
    workspace_id, atom_id, segment_id, body, kind, source_message_seqs,
    source_tool, tool_trust_class, content_hash, visibility, channel_id, publish_seq
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'channel', $10, $11);

-- name: ChannelMigrationGateOpen :one
SELECT EXISTS (
    SELECT 1 FROM memory_read_phase_gate
    WHERE workspace_id = $1 AND channel_migration_enabled AND retraction_canary_ok
);

-- name: SetMemoryReadPhaseGateChannelMigration :exec
-- Operator-approved opt-in for the channel-migration copy route (Task 16).
UPDATE memory_read_phase_gate
SET channel_migration_enabled = $2, updated_at = now()
WHERE workspace_id = $1;

-- name: ListGraphMemoryMigrationRedirectsByOldID :many
-- Forward resolution: a deletion entering through OLD canonical refs must
-- reach the NEW copies too.
SELECT new_kind, new_id FROM graph_memory_migration_redirect
WHERE workspace_id = $1 AND old_kind = $2
  AND old_id = ANY(sqlc.arg(old_ids)::text[]);

-- ===========================================================================
-- Task 17: versioned retention, encrypted archive, restore leases, sweeps.
-- ===========================================================================

-- name: EnsureBootstrapMemoryRetentionPolicy :execrows
-- New workspaces bind to the explicit bootstrap version 1
-- (90/365/30/30); no runtime default silently creates or lengthens
-- retention. Diagnostic thinking binds to the platform ceiling from the
-- start (spec §12.2).
INSERT INTO memory_retention_policy (workspace_id, version, trajectory_hot_days, archive_days, trace_hot_days, diagnostic_thinking_days, updated_by)
SELECT sqlc.arg(workspace_id)::uuid, 1, 90, 365, 30, 30, 'bootstrap'
WHERE NOT EXISTS (
    SELECT 1 FROM memory_retention_policy WHERE workspace_id = sqlc.arg(workspace_id)::uuid
);

-- name: CurrentMemoryRetentionPolicy :one
SELECT workspace_id, version, trajectory_hot_days, archive_days, trace_hot_days, diagnostic_thinking_days, updated_by, created_at
FROM memory_retention_policy
WHERE workspace_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: InsertMemoryRetentionPolicy :one
INSERT INTO memory_retention_policy (
    workspace_id, version, trajectory_hot_days, archive_days, trace_hot_days, diagnostic_thinking_days, updated_by
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING workspace_id, version, trajectory_hot_days, archive_days, trace_hot_days, diagnostic_thinking_days, updated_by, created_at;

-- name: ArchiveDueGraphMemoryBlobs :many
-- Active blobs backing at least one open graph_source ref, older than the
-- caller's policy cutoff: the hot physical trajectory/source layer.
SELECT b.id, b.workspace_id, b.storage_url, b.blob_sha256, b.size_bytes, b.created_at
FROM graph_memory_blob b
WHERE b.workspace_id = $1
  AND b.status = 'active'
  AND b.created_at <= $2
  AND EXISTS (
      SELECT 1 FROM graph_memory_blob_ref r
      WHERE r.blob_id = b.id AND r.released_at IS NULL AND r.ref_kind = 'graph_source'
  )
  AND NOT EXISTS (
      SELECT 1 FROM memory_archive_manifest m
      WHERE m.blob_id = b.id AND m.workspace_id = b.workspace_id
  )
LIMIT sqlc.arg(limit_count);

-- name: InsertMemoryArchiveManifest :one
INSERT INTO memory_archive_manifest (
    workspace_id, blob_id, object_ref, key_envelope, cipher_sha256, size_bytes, erase_due_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, workspace_id, blob_id, object_ref, key_envelope, cipher_sha256, size_bytes, status, archived_at, erase_due_at, erased_at;

-- name: RetireGraphMemoryBlob :execrows
UPDATE graph_memory_blob
SET status = 'retired', retired_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = 'active';

-- name: ReleaseGraphMemoryBlobRefs :execrows
UPDATE graph_memory_blob_ref
SET released_at = now()
WHERE blob_id = $1 AND released_at IS NULL;

-- name: ListMemoryArchiveManifestsDue :many
SELECT id, workspace_id, blob_id, object_ref, key_envelope, cipher_sha256, size_bytes, status, archived_at, erase_due_at, erased_at
FROM memory_archive_manifest
WHERE status = 'archived' AND erase_due_at <= $1
  AND NOT EXISTS (
      SELECT 1 FROM memory_archive_restore_lease l
      WHERE l.manifest_id = memory_archive_manifest.id AND l.expires_at > now()
  )
LIMIT sqlc.arg(limit_count);

-- name: EraseMemoryArchiveManifest :execrows
UPDATE memory_archive_manifest
SET status = 'erased', erased_at = now()
WHERE id = $1 AND status = 'archived';

-- name: TightenMemoryArchiveEraseDue :execrows
-- Shortened policy: existing eligible rows may only ever have their
-- erase deadline TIGHTENED (LEAST), never extended past the originally
-- bound date.
UPDATE memory_archive_manifest
SET erase_due_at = sqlc.arg(erase_due)::timestamptz
WHERE workspace_id = sqlc.arg(workspace_id)::uuid
  AND status = 'archived'
  AND erase_due_at > sqlc.arg(erase_due)::timestamptz;

-- name: GetMemoryArchiveManifest :one
SELECT id, workspace_id, blob_id, object_ref, key_envelope, cipher_sha256, size_bytes, status, archived_at, erase_due_at, erased_at
FROM memory_archive_manifest
WHERE id = $1 AND workspace_id = $2;

-- name: InsertMemoryArchiveRestoreLease :one
INSERT INTO memory_archive_restore_lease (workspace_id, manifest_id, actor, reason, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, workspace_id, manifest_id, actor, reason, expires_at, created_at;

-- name: UpsertMemoryRetentionSweepCursor :exec
INSERT INTO memory_retention_sweep_cursor (
    workspace_id, last_trajectory_sweep_at, last_trace_sweep_at, last_archive_sweep_at, last_thinking_sweep_at, updated_at
) VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (workspace_id) DO UPDATE SET
    last_trajectory_sweep_at = EXCLUDED.last_trajectory_sweep_at,
    last_trace_sweep_at = EXCLUDED.last_trace_sweep_at,
    last_archive_sweep_at = EXCLUDED.last_archive_sweep_at,
    last_thinking_sweep_at = EXCLUDED.last_thinking_sweep_at,
    updated_at = now();

-- name: ListMemoryRetentionWorkspaceIDs :many
-- Every workspace bound to at least one policy version.
SELECT DISTINCT workspace_id FROM memory_retention_policy;

-- name: ReportDueDiagnosticThinking :one
-- Dry-run/report mode for the diagnostic thinking sweep (spec §12.2):
-- counts messages whose content would be erased, without erasing.
SELECT COUNT(*)::integer AS due_count
FROM task_message m
JOIN agent_inbox_event e ON e.id = m.task_id
WHERE e.workspace_id = $1
  AND m.type = 'thinking'
  AND m.visibility = 'diagnostic_only'
  AND m.created_at < $2
  AND (COALESCE(m.content, '') <> '' OR COALESCE(m.output, '') <> '' OR m.input IS NOT NULL);

-- name: EraseDueDiagnosticThinking :execrows
-- In-place content erase for expired diagnostic provider thinking (spec
-- §12.2): the row, type, seq, and timestamps stay intact so sanitized
-- trajectory sequences remain contiguous; only the payload fields are
-- cleared. Idempotent — already-erased rows no longer match.
UPDATE task_message m
SET content = '', output = '', input = NULL
FROM agent_inbox_event e
WHERE e.id = m.task_id
  AND e.workspace_id = $1
  AND m.type = 'thinking'
  AND m.visibility = 'diagnostic_only'
  AND m.created_at < $2
  AND (COALESCE(m.content, '') <> '' OR COALESCE(m.output, '') <> '' OR m.input IS NOT NULL);

-- name: IsMemoryArchiveFenced :one
-- Task 8A fence over archive restore (spec AC 62): the manifest's blob
-- carries graph_source refs; if any of their sources is fenced in the
-- retraction registry, the archive body must never stream again.
SELECT EXISTS (
    SELECT 1
    FROM memory_archive_manifest m
    JOIN graph_memory_blob_ref r ON r.blob_id = m.blob_id
    JOIN memory_source_guard g
      ON g.workspace_id = m.workspace_id
     AND g.source_id = r.ref_id::text
     AND g.retracted_at IS NOT NULL
    WHERE m.id = $1 AND m.workspace_id = $2
);

-- ===========================================================================
-- Task 21: audited shadow-gate phase registry (spec 15/16/19, AC51/52).
-- Global-scope gates share the table through the nil-uuid sentinel workspace.
-- ===========================================================================

-- name: GetUniversalDAGShadowGateForUpdate :one
-- Locks the gate row for the caller's audited CAS transition.
SELECT scope, workspace_id, gate_name, phase, gate_version, policy_version,
       evidence, updated_by, created_at, updated_at
FROM universal_dag_shadow_gate
WHERE scope = sqlc.arg(scope)
  AND workspace_id = sqlc.arg(workspace_id)
  AND gate_name = sqlc.arg(gate_name)
FOR UPDATE;

-- name: GetUniversalDAGShadowGate :one
SELECT scope, workspace_id, gate_name, phase, gate_version, policy_version,
       evidence, updated_by, created_at, updated_at
FROM universal_dag_shadow_gate
WHERE scope = sqlc.arg(scope)
  AND workspace_id = sqlc.arg(workspace_id)
  AND gate_name = sqlc.arg(gate_name);

-- name: ListUniversalDAGShadowGatesForWorkspace :many
-- Every gate row one workspace's governance view needs: its own routes plus
-- the global training gates (nil-uuid sentinel scope).
SELECT scope, workspace_id, gate_name, phase, gate_version, policy_version,
       evidence, updated_by, created_at, updated_at
FROM universal_dag_shadow_gate
WHERE (scope = 'workspace' AND workspace_id = sqlc.arg(workspace_id))
   OR scope = 'global'
ORDER BY scope, gate_name;

-- name: RegisterUniversalDAGShadowGate :execrows
-- Creates the default-disabled row at version 0; conflicts are no-ops so the
-- register step is idempotent inside a promotion transaction.
INSERT INTO universal_dag_shadow_gate (scope, workspace_id, gate_name, updated_by)
VALUES (sqlc.arg(scope), sqlc.arg(workspace_id), sqlc.arg(gate_name), sqlc.arg(updated_by))
ON CONFLICT DO NOTHING;

-- name: TransitionUniversalDAGShadowGate :execrows
-- The only sanctioned phase move: CAS on (gate_version, from_phase). A stale
-- expected version affects zero rows and the caller must report a conflict.
UPDATE universal_dag_shadow_gate
SET phase = sqlc.arg(to_phase),
    gate_version = universal_dag_shadow_gate.gate_version + 1,
    policy_version = sqlc.arg(policy_version),
    evidence = sqlc.arg(evidence)::jsonb,
    updated_by = sqlc.arg(updated_by),
    updated_at = now()
WHERE scope = sqlc.arg(scope)
  AND workspace_id = sqlc.arg(workspace_id)
  AND gate_name = sqlc.arg(gate_name)
  AND gate_version = sqlc.arg(expected_version)
  AND phase = sqlc.arg(from_phase);

-- name: InsertUniversalDAGGateTransition :exec
-- Append-only audit row for every promotion, auto-shutdown and failure
-- demotion (spec 15).
INSERT INTO universal_dag_gate_transition (
    scope, workspace_id, gate_name, from_phase, to_phase,
    reason, trigger, evidence, policy_version, actor
) VALUES (
    sqlc.arg(scope), sqlc.arg(workspace_id), sqlc.arg(gate_name),
    sqlc.arg(from_phase), sqlc.arg(to_phase),
    sqlc.arg(reason), sqlc.arg(trigger), sqlc.arg(evidence)::jsonb,
    sqlc.arg(policy_version), sqlc.arg(actor)
);

-- name: ListUniversalDAGGateTransitions :many
-- Recent transitions of one workspace (its own scope plus global gates).
SELECT transition_id, scope, workspace_id, gate_name, from_phase, to_phase,
       reason, trigger, evidence, policy_version, actor, created_at
FROM universal_dag_gate_transition
WHERE (scope = 'workspace' AND workspace_id = sqlc.arg(workspace_id))
   OR scope = 'global'
ORDER BY transition_id DESC
LIMIT sqlc.arg(limit_rows);
