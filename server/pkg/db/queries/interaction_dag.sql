-- name: UpsertInteractionDAGSessionRun :exec
-- Idempotent on session_id: a retry that re-opens a session re-binds it to the
-- latest agent_run_id (= task.ID, D8) + issue_id. agent_run_id is the multica
-- agent_inbox_event PK (attempt-level), NOT the agent UUID.
INSERT INTO interaction_dag_session_run (session_id, project_id, agent_run_id, issue_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (session_id) DO UPDATE SET
  project_id = EXCLUDED.project_id,
  agent_run_id = EXCLUDED.agent_run_id,
  issue_id = EXCLUDED.issue_id;

-- name: GetInteractionDAGSessionRun :one
-- Resolves agent_run_id + issue_id for a session. Used by CloseSegmentForEvent
-- to stamp the segment row without a separate task lookup. Returns no rows when
-- RecordSessionAgentRun was never called for this session.
SELECT session_id, project_id, agent_run_id, issue_id, created_at
FROM interaction_dag_session_run
WHERE session_id = $1;

-- name: InsertInteractionDAGSegmentWithSnapshot :one
-- Retained pre-canonical writers insert only the approved legacy_unverified
-- exception. A per-workspace/task counter serializes generation allocation
-- until Task 2 replaces this compatibility path with its boundary cursor.
WITH task_owner AS MATERIALIZED (
  SELECT task.id AS agent_run_id, task.workspace_id, task.channel_id,
         project.id AS project_id
  FROM agent_inbox_event AS task
  JOIN project ON project.id::text = sqlc.arg(project_id)::text
              AND project.workspace_id = task.workspace_id
  WHERE task.id = sqlc.arg(agent_run_id)
), allocated AS (
  INSERT INTO interaction_dag_segment_generation_sequence (
    workspace_id, agent_run_id, next_generation
  )
  SELECT task_owner.workspace_id, task_owner.agent_run_id,
         COALESCE((
           SELECT MAX(segment.generation) + 2
           FROM interaction_dag_segment AS segment
           WHERE segment.workspace_id = task_owner.workspace_id
             AND segment.agent_run_id = task_owner.agent_run_id
         ), 2)
  FROM task_owner
  ON CONFLICT (workspace_id, agent_run_id) DO UPDATE SET
    next_generation = GREATEST(
      interaction_dag_segment_generation_sequence.next_generation + 1,
      COALESCE((
        SELECT MAX(segment.generation) + 2
        FROM interaction_dag_segment AS segment
        WHERE segment.workspace_id = EXCLUDED.workspace_id
          AND segment.agent_run_id = EXCLUDED.agent_run_id
      ), 2)
    ),
    updated_at = now()
  RETURNING workspace_id, agent_run_id,
            (next_generation - 1)::bigint AS generation
), seg AS (
  INSERT INTO interaction_dag_segment (
    segment_id, project_id, agent_run_id, issue_id, task_id,
    trajectory_id, tensor_ref, closing_event, closing_event_target_segment,
    start_seq, end_seq, trajectory_source, trainable, trajectory,
    workspace_id, generation, project_id_at_event, channel_id_at_event,
    memory_type_at_event, graph_projection_eligible_at_event, derivative,
    trainable_eligible, content_status, provider_capture_status
  )
  SELECT
    sqlc.arg(segment_id), task_owner.project_id::text,
    task_owner.agent_run_id, sqlc.narg(issue_id), sqlc.narg(task_id),
    sqlc.narg(trajectory_id), sqlc.narg(tensor_ref), sqlc.narg(closing_event),
    sqlc.narg(closing_event_target_segment), sqlc.arg(start_seq),
    sqlc.arg(end_seq), sqlc.arg(trajectory_source),
    (sqlc.arg(trainable)::boolean AND false), sqlc.arg(trajectory), task_owner.workspace_id,
    allocated.generation, task_owner.project_id,
    task_owner.channel_id, 'legacy', false, false, false,
    'legacy_unverified', 'not_expected'
  FROM task_owner
  JOIN allocated
    ON allocated.workspace_id = task_owner.workspace_id
   AND allocated.agent_run_id = task_owner.agent_run_id
  RETURNING segment_id
)
INSERT INTO interaction_dag_env_snapshot (
  segment_id, sandbox_ids, issue_snapshot_id, env_state
)
SELECT seg.segment_id, sqlc.arg(sandbox_ids),
       sqlc.narg(issue_snapshot_id), sqlc.arg(env_state)
FROM seg
RETURNING segment_id;

-- name: GetInteractionDAGSegmentByAgentRun :one
-- Legacy-unverified payload fields are masked at the SQL boundary; callers also
-- fail closed on content_status before resolving task-message content.
SELECT segment_id, project_id, agent_run_id, issue_id, task_id,
       COALESCE(CASE WHEN content_status = 'legacy_unverified' THEN NULL ELSE trajectory_id END, 0)::bigint AS trajectory_id,
       (CASE WHEN content_status = 'legacy_unverified' THEN NULL::jsonb ELSE tensor_ref END)::jsonb AS tensor_ref,
       closing_event, closing_event_target_segment, start_seq, end_seq,
       trajectory_source,
       (CASE WHEN content_status = 'legacy_unverified' THEN false ELSE trainable END)::boolean AS trainable,
       (CASE WHEN content_status = 'legacy_unverified' THEN '[]'::jsonb ELSE trajectory END)::jsonb AS trajectory,
       created_at, workspace_id, project_id_at_event, content_status, trainable_eligible
FROM interaction_dag_segment
WHERE agent_run_id = $1
ORDER BY generation DESC, created_at DESC
LIMIT 1;

-- name: GetLastEndSeqForAgentRun :one
SELECT COALESCE(MAX(end_seq), 0)::integer AS last_end_seq
FROM interaction_dag_segment
WHERE agent_run_id = $1;

-- name: GetInteractionDAGSegmentByID :one
-- Payload fields are always absent/empty for legacy_unverified rows.
SELECT segment_id, project_id, agent_run_id, issue_id, task_id,
       COALESCE(CASE WHEN content_status = 'legacy_unverified' THEN NULL ELSE trajectory_id END, 0)::bigint AS trajectory_id,
       (CASE WHEN content_status = 'legacy_unverified' THEN NULL::jsonb ELSE tensor_ref END)::jsonb AS tensor_ref,
       closing_event, closing_event_target_segment, start_seq, end_seq,
       trajectory_source,
       (CASE WHEN content_status = 'legacy_unverified' THEN false ELSE trainable END)::boolean AS trainable,
       (CASE WHEN content_status = 'legacy_unverified' THEN '[]'::jsonb ELSE trajectory END)::jsonb AS trajectory,
       created_at, workspace_id, project_id_at_event, content_status, trainable_eligible
FROM interaction_dag_segment
WHERE segment_id = $1;

-- name: GetUniversalDAGProjectWorkspace :one
SELECT workspace_id
FROM project
WHERE id::text = sqlc.arg(project_id)::text;

-- name: ListInteractionDAGSegmentsForProject :many
SELECT segment_id, project_id, agent_run_id, issue_id, task_id,
       COALESCE(CASE WHEN content_status = 'legacy_unverified' THEN NULL ELSE trajectory_id END, 0)::bigint AS trajectory_id,
       (CASE WHEN content_status = 'legacy_unverified' THEN NULL::jsonb ELSE tensor_ref END)::jsonb AS tensor_ref,
       closing_event, closing_event_target_segment, start_seq, end_seq,
       trajectory_source,
       (CASE WHEN content_status = 'legacy_unverified' THEN false ELSE trainable END)::boolean AS trainable,
       (CASE WHEN content_status = 'legacy_unverified' THEN '[]'::jsonb ELSE trajectory END)::jsonb AS trajectory,
       created_at, workspace_id, project_id_at_event, content_status, trainable_eligible
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND project_id_at_event::text = sqlc.arg(project_id)::text
ORDER BY generation, created_at, segment_id;

-- name: ListInteractionDAGEdgesForProject :many
SELECT edge.id, edge.project_id, edge.src_segment_id, edge.dst_segment_id,
       edge.type, edge.created_at
FROM interaction_dag_edge AS edge
JOIN interaction_dag_segment AS source
  ON source.workspace_id = edge.workspace_id
 AND source.segment_id = edge.src_segment_id
JOIN interaction_dag_segment AS target
  ON target.workspace_id = edge.workspace_id
 AND target.segment_id = edge.dst_segment_id
WHERE edge.workspace_id = sqlc.arg(workspace_id)
  AND source.project_id_at_event::text = sqlc.arg(project_id)::text
  AND target.project_id_at_event::text = sqlc.arg(project_id)::text
ORDER BY edge.edge_seq, edge.id;

-- name: ListInteractionDAGSessionRunsForProject :many
-- Read-only assembly query (U8): all session_id -> agent_run_id mappings for a
-- project; feeds AssembledDag.session_to_agent_run.
SELECT session_id, project_id, agent_run_id, issue_id, created_at
FROM interaction_dag_session_run
WHERE project_id = $1;

-- name: ListInteractionDAGEnvSnapshotsForProject :many
-- Read-only assembly query (U8): all env_snapshots for a project's segments.
-- interaction_dag_env_snapshot has no project_id column, so join through
-- interaction_dag_segment on segment_id. Joined by segment_id in Go (1:1).
SELECT e.segment_id, e.sandbox_ids, e.issue_snapshot_id, e.env_state
FROM interaction_dag_env_snapshot e
JOIN interaction_dag_segment s ON s.segment_id = e.segment_id
WHERE s.workspace_id = sqlc.arg(workspace_id)
  AND s.project_id_at_event::text = sqlc.arg(project_id)::text;

-- name: InsertInteractionDAGStepReward :exec
-- InsertInteractionDAGStepReward upserts a per-step reward keyed by
-- (segment_id, seq). Re-recording a key updates score/rationale, not duplicates.
INSERT INTO interaction_dag_step_reward (segment_id, seq, score, rationale)
VALUES ($1, $2, $3, $4)
ON CONFLICT (segment_id, seq) DO UPDATE SET score = EXCLUDED.score, rationale = EXCLUDED.rationale;

-- name: ListInteractionDAGStepRewardsForProject :many
-- ListInteractionDAGStepRewardsForProject returns all step rewards for segments
-- belonging to the project (the step_reward table has no project_id column, so
-- the filter joins through interaction_dag_segment). Read-only; used by
-- InteractionDAGService.AssembleAssembledDag.
SELECT sr.segment_id, sr.seq, sr.score, sr.rationale, sr.created_at
FROM interaction_dag_step_reward sr
JOIN interaction_dag_segment s ON sr.segment_id = s.segment_id
WHERE s.workspace_id = sqlc.arg(workspace_id)
  AND s.project_id_at_event::text = sqlc.arg(project_id)::text
ORDER BY sr.segment_id, sr.seq;

-- name: CreateInteractionDAGDiagnosisRun :exec
-- Snapshots a new diagnosis run (migration 208): project/task scope, topology
-- hash, and the ordered segment IDs as jsonb. Status starts 'running'.
INSERT INTO interaction_dag_diagnosis_run (run_id, project_id, task_id, topology_hash, ordered_segment_ids, status)
VALUES ($1, $2, $3, $4, $5, 'running');

-- name: GetInteractionDAGDiagnosisRun :one
SELECT run_id, project_id, task_id, topology_hash, ordered_segment_ids, status, current_segment_ordinal, pi_session_id, last_error, created_at, updated_at, completed_at, sandbox_instance_id, capability_token_hash, execution_mode, sandbox_mode
FROM interaction_dag_diagnosis_run
WHERE run_id = $1;

-- name: GetResumableInteractionDAGDiagnosisRun :one
-- Latest still-active (provisioning/running/compacting) run for a
-- (project, task); used to resume an interrupted diagnosis instead of
-- starting over. 'provisioning' is included so a sandbox-mode run whose
-- server crashed mid-provisioning can be resumed or re-provisioned.
SELECT run_id, project_id, task_id, topology_hash, ordered_segment_ids, status, current_segment_ordinal, pi_session_id, last_error, created_at, updated_at, completed_at, sandbox_instance_id, capability_token_hash, execution_mode, sandbox_mode
FROM interaction_dag_diagnosis_run
WHERE project_id = $1 AND task_id = $2 AND status IN ('provisioning', 'running', 'compacting')
ORDER BY updated_at DESC
LIMIT 1;

-- name: GetLatestCompletedInteractionDAGDiagnosisRun :one
-- Used by idempotent on-demand requests: a completed diagnosis for the exact
-- same terminal DAG is returned rather than launching another Pi session.
SELECT run_id, project_id, task_id, topology_hash, ordered_segment_ids, status, current_segment_ordinal, pi_session_id, last_error, created_at, updated_at, completed_at, sandbox_instance_id, capability_token_hash, execution_mode, sandbox_mode
FROM interaction_dag_diagnosis_run
WHERE project_id = $1 AND task_id = $2 AND status = 'completed'
ORDER BY completed_at DESC, updated_at DESC
LIMIT 1;

-- name: GetLatestInteractionDAGDiagnosisRunForProject :one
-- Latest diagnosis run of any status for a project; backs the human-facing
-- /diagnosis/latest polling endpoint.
SELECT run_id, project_id, task_id, topology_hash, ordered_segment_ids, status, current_segment_ordinal, pi_session_id, last_error, created_at, updated_at, completed_at, sandbox_instance_id, capability_token_hash, execution_mode, sandbox_mode
FROM interaction_dag_diagnosis_run
WHERE project_id = $1
ORDER BY updated_at DESC
LIMIT 1;

-- name: FailInteractionDAGDiagnosisRun :execrows
-- CAS: only an active run can be failed; last_error is bounded by the caller.
UPDATE interaction_dag_diagnosis_run
SET status = 'failed', last_error = $2, updated_at = now()
WHERE run_id = $1 AND status IN ('provisioning', 'running', 'compacting');

-- name: SetInteractionDAGDiagnosisRunSandbox :exec
-- Records the dedicated sandbox, per-run capability token hash, and execution
-- mode for a sandbox-mode diagnosis run (migration 278).
UPDATE interaction_dag_diagnosis_run
SET sandbox_instance_id = $2, capability_token_hash = $3, execution_mode = $4,
    sandbox_mode = $5, updated_at = now()
WHERE run_id = $1;

-- name: SetInteractionDAGDiagnosisRunStatus :execrows
-- CAS: transition a run between non-terminal statuses (e.g. provisioning ->
-- running once the sandbox runtime is online).
UPDATE interaction_dag_diagnosis_run
SET status = $2, updated_at = now()
WHERE run_id = $1 AND status = $3;

-- name: CompleteInteractionDAGDiagnosisRun :execrows
-- CAS: completes only while active AND every segment checkpoint is completed,
-- so the run can never be marked done with outstanding coverage.
UPDATE interaction_dag_diagnosis_run run
SET status = 'completed', completed_at = now(), updated_at = now()
WHERE run.run_id = $1
  AND run.status IN ('running', 'compacting')
  AND NOT EXISTS (
    SELECT 1 FROM interaction_dag_diagnosis_segment s
    WHERE s.run_id = $1 AND s.status <> 'completed'
  );

-- name: CreateInteractionDAGDiagnosisSegment :exec
INSERT INTO interaction_dag_diagnosis_segment (run_id, segment_id, ordinal, status)
VALUES ($1, $2, $3, 'pending');

-- name: GetInteractionDAGDiagnosisSegment :one
SELECT run_id, segment_id, ordinal, expected_message_count, fetched_message_count, expected_reward_count, expected_reward_seqs, reward_count, next_cursor, status, created_at, updated_at, completed_at
FROM interaction_dag_diagnosis_segment
WHERE run_id = $1 AND segment_id = $2;

-- name: ListInteractionDAGDiagnosisSegments :many
-- All segment checkpoints for a run in snapshot (ordinal) order.
SELECT run_id, segment_id, ordinal, expected_message_count, fetched_message_count, expected_reward_count, expected_reward_seqs, reward_count, next_cursor, status, created_at, updated_at, completed_at
FROM interaction_dag_diagnosis_segment
WHERE run_id = $1
ORDER BY ordinal;

-- name: ListLatestCompletedInteractionDAGDiagnosisTargetsForProject :many
-- Returns the frozen assistant-turn targets for the latest completed diagnosis
-- run. The assembled DAG exposes these immutable targets so downstream clients
-- can prove exact score coverage instead of inferring it from reward counts.
WITH latest_run AS (
  SELECT run_id
  FROM interaction_dag_diagnosis_run
  WHERE project_id = $1 AND status = 'completed'
  ORDER BY completed_at DESC, updated_at DESC
  LIMIT 1
)
SELECT segment_id, expected_reward_seqs
FROM interaction_dag_diagnosis_segment
WHERE run_id = (SELECT run_id FROM latest_run)
ORDER BY ordinal;

-- name: StartInteractionDAGDiagnosisSegment :execrows
-- CAS: pending -> in_progress, recording the expected message/reward coverage.
-- A replay while already in_progress/completed matches no row (idempotency is
-- resolved by the service comparing the stored expectations).
UPDATE interaction_dag_diagnosis_segment
SET status = 'in_progress', expected_message_count = $3, expected_reward_count = $4,
    expected_reward_seqs = $5, updated_at = now()
WHERE run_id = $1 AND segment_id = $2 AND status = 'pending';

-- name: AdvanceInteractionDAGDiagnosisSegmentFetch :execrows
-- CAS: only the holder of the current cursor may advance it, and the fetched
-- count only moves forward. next_cursor is opaque (HMAC-signed by the server).
UPDATE interaction_dag_diagnosis_segment
SET next_cursor = $4, fetched_message_count = $5, updated_at = now()
WHERE run_id = $1 AND segment_id = $2
  AND status = 'in_progress'
  AND next_cursor = $3
  AND fetched_message_count <= $5;

-- name: SetInteractionDAGDiagnosisSegmentRewardCount :execrows
-- Monotonic reward-coverage counter; regressive writes match no row.
UPDATE interaction_dag_diagnosis_segment
SET reward_count = $3, updated_at = now()
WHERE run_id = $1 AND segment_id = $2 AND reward_count <= $3;

-- name: CompleteInteractionDAGDiagnosisSegment :execrows
-- CAS: completes only from in_progress once both message and reward coverage
-- are satisfied; the DB, not the model, decides completion.
UPDATE interaction_dag_diagnosis_segment
SET status = 'completed', completed_at = now(), updated_at = now()
WHERE run_id = $1 AND segment_id = $2
  AND status = 'in_progress'
  AND fetched_message_count >= expected_message_count
  AND reward_count >= expected_reward_count;


-- Mixed-RL frozen DAG queries below are run-scoped. They must never join
-- root_task_id or require dense-per-session segment coverage; those assumptions
-- remain only on the legacy AssembleAssembledDag path above.

-- name: InsertMixedRLProviderCall :one
WITH allocated AS (
  UPDATE env_dispatch_run_agent
  SET next_call_ordinal = next_call_ordinal + 1
  WHERE run_id = sqlc.arg(run_id)
    AND run_agent_id = sqlc.arg(run_agent_id)
    AND next_call_ordinal = sqlc.arg(call_ordinal)
  RETURNING next_call_ordinal - 1 AS call_ordinal
)
INSERT INTO pi_provider_call (
  call_id, run_id, run_agent_id, turn_id, pi_session_id, call_ordinal,
  provider, model, api_kind, raw_provider_request, final_assistant_message,
  normalized_trajectory, normalization_version, status, stop_reason,
  response_complete, training_eligible, areal_session_id, areal_call_id,
  request_hash, response_hash, started_at, completed_at
)
SELECT
  sqlc.arg(call_id), sqlc.arg(run_id), sqlc.arg(run_agent_id), sqlc.arg(turn_id),
  sqlc.arg(pi_session_id), allocated.call_ordinal, sqlc.arg(provider),
  sqlc.arg(model), sqlc.arg(api_kind), sqlc.arg(raw_provider_request),
  sqlc.arg(final_assistant_message), sqlc.narg(normalized_trajectory),
  NULLIF(sqlc.arg(normalization_version), ''), sqlc.arg(status),
  NULLIF(sqlc.arg(stop_reason), ''), sqlc.arg(response_complete),
  (
    sqlc.arg(status)::text = 'completed'
    AND sqlc.arg(response_complete)::boolean
    AND sqlc.arg(stop_reason)::text IN ('stop', 'toolUse')
  ),
  NULLIF(sqlc.arg(areal_session_id), ''), NULLIF(sqlc.arg(areal_call_id), ''),
  sqlc.arg(request_hash), NULLIF(sqlc.arg(response_hash), ''),
  sqlc.arg(started_at), sqlc.narg(completed_at)
FROM allocated
RETURNING pi_provider_call.*;

-- name: GetMixedRLProviderCall :one
SELECT * FROM pi_provider_call
WHERE run_id = sqlc.arg(run_id) AND call_id = sqlc.arg(call_id);

-- name: ListMixedRLProviderCallsCanonical :many
SELECT call.*
FROM pi_provider_call call
JOIN env_dispatch_run_agent agent ON agent.run_agent_id = call.run_agent_id
WHERE call.run_id = sqlc.arg(run_id)
ORDER BY agent.source_agent_id, agent.run_agent_id, call.call_ordinal, call.call_id;

-- name: InsertMixedRLVisibleAction :one
INSERT INTO pi_visible_action (
  action_id, run_id, run_agent_id, turn_id, kind, canonical_id,
  producer_call_id, action_ordinal, status, created_at
) VALUES (
  sqlc.arg(action_id), sqlc.arg(run_id), sqlc.arg(run_agent_id),
  sqlc.arg(turn_id), sqlc.arg(kind), sqlc.arg(canonical_id),
  NULLIF(sqlc.arg(producer_call_id), ''), sqlc.arg(action_ordinal),
  sqlc.arg(status), sqlc.arg(created_at)
)
RETURNING *;

-- name: ListMixedRLVisibleActionsCanonical :many
SELECT * FROM pi_visible_action
WHERE run_id = sqlc.arg(run_id)
ORDER BY created_at, canonical_id;

-- name: InsertMixedRLMessageConsumption :one
INSERT INTO pi_message_consumption (
  consumption_id, run_id, run_agent_id, turn_id, channel_message_id,
  source, effective_from_call_id, consumed_at
)
SELECT sqlc.arg(consumption_id), sqlc.arg(run_id), sqlc.arg(run_agent_id),
       sqlc.arg(turn_id), sqlc.arg(channel_message_id), sqlc.arg(source),
       call.call_id, sqlc.arg(consumed_at)
FROM pi_provider_call call
WHERE call.run_id = sqlc.arg(run_id)
  AND call.run_agent_id = sqlc.arg(run_agent_id)
  AND call.call_id = sqlc.arg(effective_from_call_id)
  AND call.started_at > sqlc.arg(consumed_at)
RETURNING pi_message_consumption.*;

-- name: ListMixedRLMessageConsumptions :many
SELECT * FROM pi_message_consumption
WHERE run_id = sqlc.arg(run_id)
ORDER BY consumed_at, consumption_id;

-- name: InsertMixedRLRunSegment :one
-- Projects one frozen run-segment row. The column list and RETURNING stay on
-- the migration 315 surface so repository fixtures built from migrations
-- 313..315 keep working; the canonical mapping is written only through
-- UpsertMixedRLRunSegmentWithMapping and SetMixedRLRunSegmentUniversalMapping.
INSERT INTO interaction_dag_run_segment (
  segment_id, snapshot_id, run_id, run_agent_id, kind,
  canonical_action_id, segment_ordinal, reward, reward_source,
  provisional_at, finalized_at
) VALUES (
  sqlc.arg(segment_id), NULLIF(sqlc.arg(snapshot_id), ''), sqlc.arg(run_id),
  sqlc.arg(run_agent_id), sqlc.arg(kind), sqlc.narg(canonical_action_id),
  sqlc.arg(segment_ordinal), sqlc.narg(reward),
  NULLIF(sqlc.arg(reward_source), ''), sqlc.arg(provisional_at),
  sqlc.narg(finalized_at)
)
RETURNING segment_id, snapshot_id, run_id, run_agent_id, kind,
          canonical_action_id, segment_ordinal, reward, reward_source,
          provisional_at, finalized_at;

-- name: UpsertMixedRLRunSegmentWithMapping :one
-- Projects one frozen run-segment row in its canonical form. Re-projecting
-- the same identity is an idempotent no-op; the canonical mapping column is
-- write-once. Only valid on schemas that carry migration 465.
INSERT INTO interaction_dag_run_segment (
  segment_id, snapshot_id, run_id, run_agent_id, kind,
  canonical_action_id, segment_ordinal, reward, reward_source,
  provisional_at, finalized_at, universal_segment_id
) VALUES (
  sqlc.arg(segment_id), NULLIF(sqlc.arg(snapshot_id), ''), sqlc.arg(run_id),
  sqlc.arg(run_agent_id), sqlc.arg(kind), sqlc.narg(canonical_action_id),
  sqlc.arg(segment_ordinal), sqlc.narg(reward),
  NULLIF(sqlc.arg(reward_source), ''), sqlc.arg(provisional_at),
  sqlc.narg(finalized_at), sqlc.narg(universal_segment_id)
)
ON CONFLICT (run_id, segment_id) DO UPDATE SET
  universal_segment_id = COALESCE(
    interaction_dag_run_segment.universal_segment_id,
    EXCLUDED.universal_segment_id
  )
RETURNING segment_id, snapshot_id, run_id, run_agent_id, kind,
          canonical_action_id, segment_ordinal, reward, reward_source,
          provisional_at, finalized_at, universal_segment_id;

-- name: SetMixedRLRunSegmentUniversalMapping :execrows
-- Adopts one legacy unmapped frozen row onto its canonical Segment. The
-- mapping is write-once: an already-mapped row is never rewritten.
UPDATE interaction_dag_run_segment
SET universal_segment_id = sqlc.arg(universal_segment_id)
WHERE run_id = sqlc.arg(run_id)
  AND segment_id = sqlc.arg(segment_id)
  AND universal_segment_id IS NULL;

-- name: AssociateMixedRLProviderCall :execrows
INSERT INTO interaction_dag_segment_provider_call (
  segment_id, provider_call_id, run_id, run_agent_id,
  call_ordinal, association_kind
)
SELECT sqlc.arg(segment_id), sqlc.arg(provider_call_id), segment.run_id,
       segment.run_agent_id, call.call_ordinal, sqlc.arg(association_kind)
FROM interaction_dag_run_segment segment
JOIN pi_provider_call call
  ON call.call_id = sqlc.arg(provider_call_id)
 AND call.run_id = segment.run_id
 AND call.run_agent_id = segment.run_agent_id
 AND call.call_ordinal = sqlc.arg(call_ordinal)
WHERE segment.segment_id = sqlc.arg(segment_id)
  AND (
    sqlc.arg(association_kind)::text <> 'shared_producer'
    OR EXISTS (
      SELECT 1
      FROM interaction_dag_segment_provider_call owner
      WHERE owner.provider_call_id = call.call_id
        AND owner.run_id = segment.run_id
        AND owner.association_kind = 'owned'
    )
  );

-- name: ListMixedRLSegmentCallsCanonical :many
SELECT association.*
FROM interaction_dag_segment_provider_call association
JOIN interaction_dag_run_segment segment
  ON segment.segment_id = association.segment_id
WHERE segment.run_id = sqlc.arg(run_id)
ORDER BY segment.segment_ordinal, association.call_ordinal,
         association.provider_call_id;

-- name: AssociateMixedRLProviderCallIdempotent :execrows
-- Projects one canonical provider-call association into the frozen snapshot.
-- An identical re-run is a no-op; any drift from the stored association
-- affects zero rows and the projector fails closed on the mismatch.
INSERT INTO interaction_dag_segment_provider_call (
  segment_id, provider_call_id, run_id, run_agent_id,
  call_ordinal, association_kind
)
SELECT sqlc.arg(segment_id), sqlc.arg(provider_call_id), segment.run_id,
       segment.run_agent_id, sqlc.arg(call_ordinal), sqlc.arg(association_kind)
FROM interaction_dag_run_segment segment
JOIN pi_provider_call call
  ON call.call_id = sqlc.arg(provider_call_id)
 AND call.run_id = segment.run_id
 AND call.run_agent_id = segment.run_agent_id
 AND call.call_ordinal = sqlc.arg(call_ordinal)
WHERE segment.segment_id = sqlc.arg(segment_id)
  AND (
    sqlc.arg(association_kind)::text <> 'shared_producer'
    OR EXISTS (
      SELECT 1
      FROM interaction_dag_segment_provider_call owner
      WHERE owner.provider_call_id = call.call_id
        AND owner.run_id = segment.run_id
        AND owner.association_kind = 'owned'
    )
  )
ON CONFLICT (segment_id, provider_call_id) DO UPDATE SET
  call_ordinal = interaction_dag_segment_provider_call.call_ordinal
WHERE interaction_dag_segment_provider_call.call_ordinal = EXCLUDED.call_ordinal
  AND interaction_dag_segment_provider_call.association_kind = EXCLUDED.association_kind
  AND interaction_dag_segment_provider_call.run_id = EXCLUDED.run_id
  AND interaction_dag_segment_provider_call.run_agent_id = EXCLUDED.run_agent_id;

-- name: InsertMixedRLCausalEdge :one
-- The column list and RETURNING stay on the migration 315 surface so
-- repository fixtures built from migrations 313..315 keep working; the
-- canonical mapping is written only through
-- InsertMixedRLCausalEdgeWithUniversal.
INSERT INTO interaction_dag_causal_edge (
  edge_id, snapshot_id, run_id, src_segment_id, dst_segment_id, type,
  trigger_message_id, dst_call_id, edge_ordinal
) VALUES (
  sqlc.arg(edge_id), NULLIF(sqlc.arg(snapshot_id), ''), sqlc.arg(run_id),
  sqlc.arg(src_segment_id), sqlc.arg(dst_segment_id), sqlc.arg(type),
  sqlc.narg(trigger_message_id), NULLIF(sqlc.arg(dst_call_id), ''),
  sqlc.arg(edge_ordinal)
)
RETURNING edge_id, snapshot_id, run_id, src_segment_id, dst_segment_id, type,
          trigger_message_id, dst_call_id, edge_ordinal;

-- name: InsertMixedRLCausalEdgeWithUniversal :one
-- Projects one causal edge together with its canonical mapping. Only valid on
-- schemas that carry migration 465.
INSERT INTO interaction_dag_causal_edge (
  edge_id, snapshot_id, run_id, src_segment_id, dst_segment_id, type,
  trigger_message_id, dst_call_id, edge_ordinal, universal_edge_id
) VALUES (
  sqlc.arg(edge_id), NULLIF(sqlc.arg(snapshot_id), ''), sqlc.arg(run_id),
  sqlc.arg(src_segment_id), sqlc.arg(dst_segment_id), sqlc.arg(type),
  sqlc.narg(trigger_message_id), NULLIF(sqlc.arg(dst_call_id), ''),
  sqlc.arg(edge_ordinal), sqlc.narg(universal_edge_id)
)
RETURNING edge_id, snapshot_id, run_id, src_segment_id, dst_segment_id, type,
          trigger_message_id, dst_call_id, edge_ordinal, universal_edge_id;

-- name: ListMixedRLCausalEdgesCanonical :many
SELECT edge_id, snapshot_id, run_id, src_segment_id, dst_segment_id, type,
       trigger_message_id, dst_call_id, edge_ordinal
FROM interaction_dag_causal_edge
WHERE run_id = sqlc.arg(run_id)
ORDER BY edge_ordinal, edge_id;

-- name: CreateMixedRLFrozenSnapshot :one
INSERT INTO interaction_dag_frozen_snapshot (
  snapshot_id, run_id, run_status, schema_version, normalization_version,
  segment_count, call_count, edge_count, canonical_manifest, snapshot_hash
) VALUES (
  sqlc.arg(snapshot_id), sqlc.arg(run_id), sqlc.arg(run_status),
  sqlc.arg(schema_version), sqlc.arg(normalization_version),
  sqlc.arg(segment_count), sqlc.arg(call_count), sqlc.arg(edge_count),
  sqlc.arg(canonical_manifest), sqlc.arg(snapshot_hash)
)
RETURNING *;

-- name: GetMixedRLFrozenSnapshot :one
SELECT * FROM interaction_dag_frozen_snapshot
WHERE run_id = sqlc.arg(run_id);

-- name: ListMixedRLSnapshotSegmentsCanonical :many
SELECT segment_id, snapshot_id, run_id, run_agent_id, kind,
       canonical_action_id, segment_ordinal, reward, reward_source,
       provisional_at, finalized_at
FROM interaction_dag_run_segment
WHERE snapshot_id = sqlc.arg(snapshot_id)
ORDER BY segment_ordinal, segment_id;

-- name: ListMixedRLRunSegmentsCanonical :many
-- Provisional plus terminal segments in the deterministic order used by the
-- freeze manifest, before snapshot_id is assigned. The column list stays on
-- the migration 315 surface for repository fixtures.
SELECT segment_id, snapshot_id, run_id, run_agent_id, kind,
       canonical_action_id, segment_ordinal, reward, reward_source,
       provisional_at, finalized_at
FROM interaction_dag_run_segment
WHERE run_id = sqlc.arg(run_id)
ORDER BY segment_ordinal, segment_id;

-- name: ListMixedRLRunSegmentsWithMapping :many
-- Same ordering as ListMixedRLRunSegmentsCanonical, plus the canonical
-- mapping column for the frozen projector. Only valid on schemas that carry
-- migration 465.
SELECT segment.segment_id, segment.snapshot_id, segment.run_id,
       segment.run_agent_id, segment.kind, segment.canonical_action_id,
       segment.segment_ordinal, segment.reward, segment.reward_source,
       segment.provisional_at, segment.finalized_at,
       segment.universal_segment_id
FROM interaction_dag_run_segment AS segment
WHERE segment.run_id = sqlc.arg(run_id)
ORDER BY segment.segment_ordinal, segment.segment_id;

-- name: CountMixedRLProviderCalls :one
SELECT count(*) FROM pi_provider_call
WHERE run_id = sqlc.arg(run_id);

-- name: CountMixedRLSegments :one
SELECT count(*) FROM interaction_dag_run_segment
WHERE run_id = sqlc.arg(run_id);

-- name: CountMixedRLEdges :one
SELECT count(*) FROM interaction_dag_causal_edge
WHERE run_id = sqlc.arg(run_id);

-- name: GetChannelMessageReactionMessageID :one
-- Resolves the reacted-to channel message for a successful reaction action.
SELECT channel_message_id FROM channel_message_reaction
WHERE id = sqlc.arg(reaction_id);


-- name: AbortMixedRLUnfinishedProviderCalls :execrows
-- Timeout freeze marks every still-observable unfinished call aborted and
-- training-ineligible. Capture gaps cover turns whose batch never arrived.
UPDATE pi_provider_call
SET status = 'aborted',
    response_complete = false,
    training_eligible = false,
    stop_reason = NULL,
    completed_at = COALESCE(completed_at, sqlc.arg(completed_at))
WHERE run_id = sqlc.arg(run_id)
  AND frozen_at IS NULL
  AND status = 'in_progress';

-- name: FreezeMixedRLProviderCalls :execrows
UPDATE pi_provider_call
SET frozen_at = sqlc.arg(frozen_at)
WHERE run_id = sqlc.arg(run_id) AND frozen_at IS NULL;

-- name: FreezeMixedRLSegments :execrows
UPDATE interaction_dag_run_segment
SET snapshot_id = sqlc.arg(snapshot_id),
    finalized_at = COALESCE(finalized_at, sqlc.arg(frozen_at))
WHERE run_id = sqlc.arg(run_id) AND snapshot_id IS NULL;

-- name: FreezeMixedRLEdges :execrows
UPDATE interaction_dag_causal_edge
SET snapshot_id = sqlc.arg(snapshot_id)
WHERE run_id = sqlc.arg(run_id) AND snapshot_id IS NULL;


-- name: RecordMixedRLLateEvent :exec
INSERT INTO env_dispatch_run_audit_event (
  event_id, run_id, run_agent_id, turn_id, kind, reason, summary, snapshot_id
) VALUES (
  sqlc.arg(event_id), sqlc.arg(run_id), sqlc.narg(run_agent_id),
  sqlc.narg(turn_id), 'late_event', sqlc.arg(reason), sqlc.arg(summary),
  sqlc.arg(snapshot_id)
);

-- name: ValidateMixedRLRunForFreeze :one
SELECT
  (
    SELECT count(*)
    FROM env_dispatch_run_agent agent
    WHERE agent.run_id = sqlc.arg(run_id)
      AND agent.training_mode = 'online_rl'
      AND agent.areal_session_id IS NULL
  ) AS missing_online_session_count,
  (
    SELECT count(*)
    FROM env_dispatch_run_agent agent
    WHERE agent.run_id = sqlc.arg(run_id)
      AND (
        (agent.training_mode = 'online_rl' AND agent.areal_session_id IS NULL)
        OR (agent.training_mode = 'none' AND agent.areal_session_id IS NOT NULL)
      )
  ) AS invalid_run_agent_identity_count,
  (
    SELECT count(*)
    FROM pi_provider_call call
    JOIN env_dispatch_run_agent agent
      ON agent.run_id = call.run_id
     AND agent.run_agent_id = call.run_agent_id
    WHERE call.run_id = sqlc.arg(run_id)
      AND (
        call.pi_session_id IS DISTINCT FROM agent.pi_session_id
        OR (
          agent.training_mode = 'online_rl'
          AND (
            agent.areal_session_id IS NULL
            OR call.areal_session_id IS DISTINCT FROM agent.areal_session_id
            OR call.areal_call_id IS NULL
          )
        )
        OR (
          agent.training_mode <> 'online_rl'
          AND (call.areal_session_id IS NOT NULL OR call.areal_call_id IS NOT NULL)
        )
      )
  ) AS invalid_provider_call_identity_count,
  (
    SELECT count(*)
    FROM env_dispatch_turn_capture_batch batch
    JOIN env_dispatch_resident_turn turn ON turn.turn_id = batch.turn_id
    JOIN env_dispatch_run_agent agent
      ON agent.run_id = turn.run_id
     AND agent.run_agent_id = turn.run_agent_id
    WHERE turn.run_id = sqlc.arg(run_id)
      AND batch.capture_boundary IS DISTINCT FROM agent.capture_boundary
  ) AS capture_boundary_mismatch_count,
  (
    SELECT count(*)
    FROM interaction_dag_segment_provider_call shared
    WHERE shared.run_id = sqlc.arg(run_id)
      AND shared.association_kind = 'shared_producer'
      AND NOT EXISTS (
        SELECT 1
        FROM interaction_dag_segment_provider_call owner
        WHERE owner.run_id = shared.run_id
          AND owner.provider_call_id = shared.provider_call_id
          AND owner.association_kind = 'owned'
      )
  ) AS shared_without_owner_count,
  (
    SELECT count(*)
    FROM pi_message_consumption consumption
    LEFT JOIN pi_provider_call call
      ON call.run_id = consumption.run_id
     AND call.run_agent_id = consumption.run_agent_id
     AND call.call_id = consumption.effective_from_call_id
    WHERE consumption.run_id = sqlc.arg(run_id)
      AND (call.call_id IS NULL OR call.started_at <= consumption.consumed_at)
  ) AS invalid_consumption_count,
  (
    SELECT count(*)
    FROM (
      SELECT segment.run_agent_id
      FROM interaction_dag_run_segment segment
      WHERE segment.run_id = sqlc.arg(run_id)
        AND segment.kind = 'terminal'
      GROUP BY segment.run_agent_id
      HAVING count(*) > 1
    ) duplicate_terminal
  ) AS duplicate_terminal_agent_count,
  (
    SELECT count(*)
    FROM env_dispatch_resident_turn turn
    WHERE turn.run_id = sqlc.arg(run_id)
      AND turn.status = 'settled'
      AND NOT EXISTS (
        SELECT 1
        FROM env_dispatch_turn_capture_batch batch
        WHERE batch.turn_id = turn.turn_id
      )
      AND NOT EXISTS (
        SELECT 1
        FROM env_dispatch_run_audit_event gap
        WHERE gap.run_id = turn.run_id
          AND gap.run_agent_id = turn.run_agent_id
          AND gap.turn_id = turn.turn_id
          AND gap.kind = 'capture_gap'
      )
  ) AS uncovered_settled_turn_count;

-- name: LockUniversalDAGTaskCursor :one
-- Locks the canonical per-task generation cursor before allocating or closing a range.
SELECT *
FROM interaction_dag_task_cursor
WHERE workspace_id = sqlc.arg(workspace_id)
  AND agent_run_id = sqlc.arg(agent_run_id)
FOR UPDATE;

-- name: UpsertUniversalDAGTaskCursor :one
-- Creates or replaces the canonical generation and open-range state for one task.
INSERT INTO interaction_dag_task_cursor (
  workspace_id, agent_run_id, next_generation, open_start_seq,
  last_closed_seq, open_generation, open_end_seq
) VALUES (
  sqlc.arg(workspace_id), sqlc.arg(agent_run_id), sqlc.arg(next_generation),
  sqlc.narg(open_start_seq), sqlc.arg(last_closed_seq),
  sqlc.narg(open_generation), sqlc.narg(open_end_seq)
)
ON CONFLICT (workspace_id, agent_run_id) DO UPDATE SET
  next_generation = EXCLUDED.next_generation,
  open_start_seq = EXCLUDED.open_start_seq,
  last_closed_seq = EXCLUDED.last_closed_seq,
  open_generation = EXCLUDED.open_generation,
  open_end_seq = EXCLUDED.open_end_seq,
  updated_at = now()
RETURNING *;

-- name: InsertUniversalDAGSegment :one
-- Inserts canonical workspace-scoped Segment metadata backed by task_messages.
INSERT INTO interaction_dag_segment (
  segment_id, workspace_id, agent_run_id, generation, issue_id,
  start_seq, end_seq, trajectory_source, trainable, trajectory,
  project_id_at_event, channel_id_at_event, route_generation_at_event,
  memory_type_at_event, graph_projection_eligible_at_event,
  close_action_kind, canonical_action_id, visible_action_key,
  derivative, trainable_eligible, publish_status, content_status,
  sanitizer_version, policy_version, provider_capture_status,
  provider_capture_correlation_key, run_id, run_agent_id, boundary_quality
) VALUES (
  sqlc.arg(segment_id), sqlc.arg(workspace_id), sqlc.arg(agent_run_id),
  sqlc.arg(generation), NULLIF(sqlc.arg(issue_id)::text, ''),
  sqlc.arg(start_seq), sqlc.arg(end_seq), 'task_messages',
  sqlc.arg(trainable_eligible), '[]'::jsonb,
  sqlc.narg(project_id_at_event), sqlc.narg(channel_id_at_event),
  sqlc.narg(route_generation_at_event), sqlc.arg(memory_type_at_event),
  sqlc.arg(graph_projection_eligible_at_event), sqlc.arg(close_action_kind)::text,
  sqlc.narg(canonical_action_id), NULLIF(sqlc.arg(visible_action_key)::text, ''),
  sqlc.arg(derivative), sqlc.arg(trainable_eligible),
  'pending',
  CASE WHEN sqlc.arg(close_action_kind)::text = 'metadata_only' THEN 'empty' ELSE 'pending' END,
  NULLIF(sqlc.arg(sanitizer_version)::text, ''), NULLIF(sqlc.arg(policy_version)::text, ''),
  CASE
    WHEN NULLIF(sqlc.arg(provider_capture_correlation_key)::text, '') IS NULL
      THEN 'not_expected'
    ELSE 'pending'
  END,
  NULLIF(sqlc.arg(provider_capture_correlation_key)::text, ''), sqlc.narg(run_id),
  sqlc.narg(run_agent_id), NULLIF(sqlc.arg(boundary_quality)::text, '')
)
RETURNING *;

-- name: InsertUniversalDAGPublishOutbox :one
-- Persists the initial durable publication request and retry schedule for a Segment.
INSERT INTO interaction_dag_publish_outbox (
  workspace_id, segment_id, request_hash, status, attempts, next_attempt_at
) VALUES (
  sqlc.arg(workspace_id), sqlc.arg(segment_id), sqlc.arg(request_hash),
  'pending', 0, sqlc.narg(next_attempt_at)
)
RETURNING *;

-- name: ClaimUniversalDAGPublishOutbox :many
-- Atomically leases claimable publish work: pending rows, retry rows whose
-- backoff elapsed, and processing rows whose lease expired. Rows leased by a
-- live worker are skipped, so concurrent publishers claim disjoint sets. The
-- Segment publish lifecycle moves to processing in the same transaction via
-- MarkUniversalDAGSegmentPublishProcessing.
WITH claimable AS (
  SELECT workspace_id, segment_id
  FROM interaction_dag_publish_outbox
  WHERE status = 'pending'
     OR (status = 'retry' AND next_attempt_at <= now())
     OR (status = 'processing' AND lease_expires_at < now())
  ORDER BY updated_at, segment_id
  LIMIT sqlc.arg(max_rows)
  FOR UPDATE SKIP LOCKED
), leased AS (
  UPDATE interaction_dag_publish_outbox AS outbox
  SET status = 'processing',
      lease_owner = sqlc.arg(lease_owner),
      lease_expires_at = sqlc.arg(lease_expires_at),
      next_attempt_at = NULL,
      updated_at = now()
  FROM claimable
  WHERE outbox.workspace_id = claimable.workspace_id
    AND outbox.segment_id = claimable.segment_id
  RETURNING outbox.workspace_id, outbox.segment_id, outbox.request_hash, outbox.attempts
)
SELECT leased.workspace_id, leased.segment_id, leased.request_hash, leased.attempts,
       segment.agent_run_id, segment.generation, segment.start_seq, segment.end_seq,
       segment.close_action_kind, segment.memory_type_at_event,
       segment.graph_projection_eligible_at_event, segment.derivative, segment.trainable_eligible,
       segment.channel_id_at_event, segment.project_id_at_event, segment.route_generation_at_event
FROM leased
JOIN interaction_dag_segment AS segment
  ON segment.workspace_id = leased.workspace_id
 AND segment.segment_id = leased.segment_id
ORDER BY leased.segment_id;

-- name: MarkUniversalDAGSegmentPublishProcessing :execrows
-- Mirrors an outbox claim onto the Segment publish lifecycle. Rows already
-- processing (stale-lease steal) legitimately affect zero rows.
UPDATE interaction_dag_segment
SET publish_status = 'processing', updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND publish_status IN ('pending', 'retry');

-- name: GetUniversalDAGPublishOutboxForUpdate :one
-- Locks one outbox row for the outcome transaction so lease ownership can be
-- re-verified before any lifecycle transition is applied.
SELECT * FROM interaction_dag_publish_outbox
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
FOR UPDATE;

-- name: RetryUniversalDAGPublishOutbox :execrows
-- Records one transient failure: attempts must advance by exactly one and the
-- lease is released. The attempts guard makes the increment a compare-and-swap
-- against concurrent lease stealers.
UPDATE interaction_dag_publish_outbox
SET status = 'retry', attempts = attempts + 1,
    lease_owner = NULL, lease_expires_at = NULL,
    next_attempt_at = sqlc.arg(next_attempt_at),
    last_error = sqlc.arg(last_error), updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND status = 'processing'
  AND attempts = sqlc.arg(current_attempts);

-- name: RetryUniversalDAGSegment :execrows
UPDATE interaction_dag_segment
SET publish_status = 'retry', updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND publish_status = 'processing';

-- name: FailUniversalDAGPublishOutbox :execrows
-- Moves a claimed row straight to a terminal failure state. Legal values are
-- redaction_failed, rejected_scope, and dead_letter; the lifecycle trigger
-- rejects anything unreachable from processing.
UPDATE interaction_dag_publish_outbox
SET status = sqlc.arg(terminal_status)::text,
    lease_owner = NULL, lease_expires_at = NULL, next_attempt_at = NULL,
    completed_at = now(), last_error = sqlc.arg(last_error), updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND status = 'processing';

-- name: FailUniversalDAGSegment :execrows
-- Mirrors a terminal publish outcome. Metadata-only Segments keep their
-- empty content status; only pending content takes the terminal value.
UPDATE interaction_dag_segment
SET publish_status = sqlc.arg(terminal_status)::text,
    content_status = CASE
      WHEN content_status = 'pending' THEN sqlc.arg(terminal_status)::text
      ELSE content_status
    END,
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND publish_status = 'processing';

-- name: LockUniversalDAGPublishSequence :exec
-- Serializes per-workspace publish_seq allocation inside the outcome
-- transaction without a dedicated allocator table.
SELECT pg_advisory_xact_lock(
  hashtext('universal-dag-publish-seq:' || sqlc.arg(workspace_key))
);

-- name: NextUniversalDAGPublishSeq :one
SELECT COALESCE(MAX(publish_seq), 0) + 1 AS next_publish_seq
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: PublishUniversalDAGPublishOutbox :execrows
UPDATE interaction_dag_publish_outbox
SET status = 'published', lease_owner = NULL, lease_expires_at = NULL,
    next_attempt_at = NULL, completed_at = now(), updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND status = 'processing';

-- name: PublishUniversalDAGSegment :execrows
-- Completes the publish transaction: the sequence is allocated only here,
-- after the payload sink succeeded, together with the sanitized trajectory
-- document and its sanitizer/policy identity. Metadata-only Segments stay
-- empty ('[]'); a redaction failure never reaches this statement.
UPDATE interaction_dag_segment
SET publish_status = 'published',
    publish_seq = sqlc.arg(publish_seq),
    published_at = now(),
    trajectory = sqlc.arg(trajectory)::jsonb,
    sanitizer_version = NULLIF(sqlc.arg(sanitizer_version)::text, ''),
    policy_version = NULLIF(sqlc.arg(policy_version)::text, ''),
    content_status = CASE
      WHEN content_status = 'pending' THEN 'published'
      ELSE content_status
    END,
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND publish_status = 'processing';

-- name: RequeueUniversalDAGPublishOutbox :execrows
-- DLQ replay for recoverable rows: pulls a retrying row's next attempt to now
-- without touching its attempt ledger.
UPDATE interaction_dag_publish_outbox
SET next_attempt_at = now(), updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND status = 'retry';

-- name: GetUniversalDAGPublishOutboxStatus :one
SELECT status FROM interaction_dag_publish_outbox
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id);

-- name: UniversalDAGPublishHealth :one
-- Aggregate outbox counters backing Workspace health reporting.
SELECT
  count(*) FILTER (WHERE status = 'pending') AS pending_count,
  count(*) FILTER (WHERE status = 'processing' AND lease_expires_at > now()) AS leased_count,
  count(*) FILTER (WHERE status = 'processing' AND lease_expires_at <= now()) AS stale_leased_count,
  count(*) FILTER (WHERE status = 'retry') AS retry_count,
  count(*) FILTER (WHERE status = 'published') AS published_count,
  count(*) FILTER (WHERE status = 'redaction_failed') AS redaction_failed_count,
  count(*) FILTER (WHERE status = 'rejected_scope') AS rejected_scope_count,
  count(*) FILTER (WHERE status = 'dead_letter') AS dead_letter_count,
  count(*) FILTER (WHERE status = 'retracted') AS retracted_count
FROM interaction_dag_publish_outbox;

-- name: InsertUniversalDAGProviderCallLink :one
-- Idempotently associates a finalized provider call with its canonical Segment.
INSERT INTO interaction_dag_universal_provider_call (
  segment_id, provider_call_id, role, ordinal, run_id, run_agent_id, capture_id
) VALUES (
  sqlc.arg(segment_id), sqlc.arg(provider_call_id), sqlc.arg(role),
  sqlc.arg(ordinal), sqlc.arg(run_id), sqlc.arg(run_agent_id),
  sqlc.arg(capture_id)
)
ON CONFLICT (segment_id, provider_call_id) DO UPDATE SET
  segment_id = EXCLUDED.segment_id
WHERE interaction_dag_universal_provider_call.role = EXCLUDED.role
  AND interaction_dag_universal_provider_call.ordinal = EXCLUDED.ordinal
  AND interaction_dag_universal_provider_call.run_id = EXCLUDED.run_id
  AND interaction_dag_universal_provider_call.run_agent_id = EXCLUDED.run_agent_id
  AND interaction_dag_universal_provider_call.capture_id = EXCLUDED.capture_id
RETURNING *;

-- name: FinalizeUniversalDAGProviderCapture :one
-- Finalizes a pending provider capture while allowing an identical retry to succeed.
UPDATE interaction_dag_segment
SET provider_capture_status = 'finalized',
    provider_capture_id = sqlc.arg(capture_id)::text,
    provider_capture_version = sqlc.arg(capture_version)::bigint,
    updated_at = CASE
      WHEN provider_capture_status = 'pending' THEN now()
      ELSE updated_at
    END
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND provider_capture_correlation_key = sqlc.arg(provider_capture_correlation_key)::text
  AND (
    provider_capture_status = 'pending'
    OR (
      provider_capture_status = 'finalized'
      AND provider_capture_id = sqlc.arg(capture_id)::text
      AND provider_capture_version = sqlc.arg(capture_version)::bigint
    )
  )
RETURNING *;

-- name: MarkUniversalDAGProviderCaptureConflict :exec
-- Marks a pending or finalized provider capture as conflicted without replacing prior identity.
UPDATE interaction_dag_segment
SET provider_capture_status = 'conflict',
    provider_capture_id = COALESCE(
      provider_capture_id, sqlc.arg(capture_id)::text
    ),
    provider_capture_version = COALESCE(
      provider_capture_version, sqlc.arg(capture_version)::bigint
    ),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND provider_capture_correlation_key = sqlc.arg(provider_capture_correlation_key)::text
  AND provider_capture_status IN ('pending', 'finalized');

-- name: GetUniversalDAGSegment :one
-- Reads one complete canonical Segment by its workspace-scoped identity.
SELECT *
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id)
  AND content_status <> 'legacy_unverified';

-- name: InsertUniversalDAGEdge :one
-- Inserts a canonical typed Edge with the required durable trigger identity semantics.
INSERT INTO interaction_dag_edge (
  workspace_id, edge_seq, src_segment_id, dst_segment_id, type,
  trigger_message_id
)
SELECT
  sqlc.arg(workspace_id), sqlc.arg(edge_seq), sqlc.arg(src_segment_id),
  sqlc.arg(dst_segment_id), sqlc.arg(edge_type),
  sqlc.narg(trigger_message_id)
WHERE (
    sqlc.arg(edge_type)::text = 'continues'
    AND sqlc.narg(trigger_message_id)::uuid IS NULL
  ) OR (
    sqlc.arg(edge_type)::text IN ('responds_to', 'delegates_to', 'mentions')
  )
RETURNING *;

-- name: AllocateUniversalDAGEdgeSeq :one
-- Atomically reserves a workspace edge sequence. Canonical AddEdge uses the
-- combined insert below so a failed insert cannot consume a sequence.
INSERT INTO interaction_dag_edge_sequence (workspace_id, next_edge_seq)
VALUES (
  sqlc.arg(workspace_id),
  COALESCE((SELECT MAX(edge_seq) + 2 FROM interaction_dag_edge
            WHERE workspace_id = sqlc.arg(workspace_id)), 2)
)
ON CONFLICT (workspace_id) DO UPDATE SET
  next_edge_seq = GREATEST(
    interaction_dag_edge_sequence.next_edge_seq + 1,
    COALESCE((SELECT MAX(edge_seq) + 2 FROM interaction_dag_edge
              WHERE workspace_id = sqlc.arg(workspace_id)), 2)
  ),
  updated_at = now()
RETURNING (next_edge_seq - 1)::bigint AS edge_seq;

-- name: InsertUniversalDAGEdgeAtomic :one
-- Resolves durable trigger provenance, allocates edge_seq, and inserts in one
-- statement. The sequence-row update serializes concurrent workspace writers.
WITH source AS MATERIALIZED (
  SELECT segment.workspace_id, segment.segment_id, segment.agent_run_id,
         segment.start_seq, segment.end_seq, trigger.id AS trigger_message_id
  FROM interaction_dag_segment AS segment
  LEFT JOIN LATERAL (
    SELECT message.id
    FROM task_message AS message
    WHERE message.task_id = segment.agent_run_id
      AND message.seq BETWEEN segment.start_seq AND segment.end_seq
    ORDER BY message.seq DESC, message.id DESC
    LIMIT 1
  ) AS trigger ON sqlc.arg(edge_type)::text <> 'continues'
  WHERE segment.workspace_id = sqlc.arg(workspace_id)
    AND segment.segment_id = sqlc.arg(src_segment_id)
    AND (
      sqlc.arg(edge_type)::text = 'continues'
      OR trigger.id IS NOT NULL
    )
), allocated AS (
  INSERT INTO interaction_dag_edge_sequence (workspace_id, next_edge_seq)
  SELECT source.workspace_id,
         COALESCE((SELECT MAX(edge_seq) + 2 FROM interaction_dag_edge
                   WHERE workspace_id = source.workspace_id), 2)
  FROM source
  ON CONFLICT (workspace_id) DO UPDATE SET
    next_edge_seq = GREATEST(
      interaction_dag_edge_sequence.next_edge_seq + 1,
      COALESCE((SELECT MAX(edge_seq) + 2 FROM interaction_dag_edge
                WHERE workspace_id = EXCLUDED.workspace_id), 2)
    ),
    updated_at = now()
  RETURNING next_edge_seq - 1 AS edge_seq, workspace_id
)
INSERT INTO interaction_dag_edge (
  workspace_id, edge_seq, src_segment_id, dst_segment_id, type,
  trigger_message_id
)
SELECT allocated.workspace_id, allocated.edge_seq, source.segment_id,
       sqlc.arg(dst_segment_id), sqlc.arg(edge_type),
       CASE WHEN sqlc.arg(edge_type)::text = 'continues'
            THEN NULL::uuid ELSE source.trigger_message_id END
FROM allocated
JOIN source ON source.workspace_id = allocated.workspace_id
RETURNING interaction_dag_edge.*;

-- name: GetUniversalDAGEdgeTriggerMessageID :one
-- Resolves the source Segment's final persisted task-message identity. Legacy
-- seam writers do not yet carry canonical_action_id, so their closing visible
-- action is the final message inside the source Segment's frozen range.
SELECT message.id
FROM interaction_dag_segment AS segment
JOIN task_message AS message
  ON message.task_id = segment.agent_run_id
 AND message.seq BETWEEN segment.start_seq AND segment.end_seq
WHERE segment.workspace_id = sqlc.arg(workspace_id)
  AND segment.segment_id = sqlc.arg(segment_id)
ORDER BY message.seq DESC, message.id DESC
LIMIT 1;

-- name: UniversalDAGStorePresent :one
-- Reports whether the fully-projected canonical Universal DAG store exists in
-- this database. The migration 465 mapping column is the discriminator: the
-- legacy interaction_dag_segment table pre-dates 454 and still exists in
-- migrations-313..315 repository fixtures, whose frozen snapshots keep running
-- the legacy semantics instead of failing on a missing canonical source of
-- truth.
SELECT EXISTS (
  SELECT 1
  FROM pg_catalog.pg_attribute AS attribute
  JOIN pg_catalog.pg_class AS class ON class.oid = attribute.attrelid
  JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = class.relnamespace
  WHERE class.relname = 'interaction_dag_run_segment'
    AND attribute.attname = 'universal_segment_id'
    AND NOT attribute.attisdropped
    AND namespace.nspname = ANY (string_to_array(current_setting('search_path'), ','))
) AS store_present;

-- name: ListUniversalDAGSegmentsByRun :many
-- Lists complete canonical Segments for one dispatch run in deterministic agent/generation order.
SELECT *
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND run_id = sqlc.arg(run_id)
  AND content_status <> 'legacy_unverified'
ORDER BY run_agent_id, agent_run_id, generation, segment_id;

-- name: ListUniversalDAGProviderCallLinksByRun :many
-- Lists every canonical provider-call association of one dispatch run in a
-- deterministic order for frozen-projection mirroring.
SELECT *
FROM interaction_dag_universal_provider_call
WHERE run_id = sqlc.arg(run_id)
ORDER BY run_agent_id, ordinal, provider_call_id;

-- name: FindUniversalDAGEdgesByTriggerAndDestination :many
-- Resolves the canonical Edge candidates that share a frozen causal edge's
-- trigger message and destination endpoint for deterministic edge mapping.
SELECT id
FROM interaction_dag_edge
WHERE workspace_id = sqlc.arg(workspace_id)
  AND trigger_message_id = sqlc.arg(trigger_message_id)
  AND dst_segment_id = sqlc.arg(dst_segment_id)
ORDER BY id;

-- name: ListUniversalDAGEdgesAtWatermark :many
-- Lists canonical Edges visible at a fixed workspace Edge watermark.
SELECT *
FROM interaction_dag_edge
WHERE workspace_id = sqlc.arg(workspace_id)
  AND edge_seq <= sqlc.arg(edge_seq_max)
ORDER BY edge_seq, id;

-- name: EnsureUniversalDAGTaskCursor :exec
-- Creates the first cursor state without overwriting a concurrent writer.
INSERT INTO interaction_dag_task_cursor (
  workspace_id, agent_run_id, next_generation, last_closed_seq
) VALUES (
  sqlc.arg(workspace_id), sqlc.arg(agent_run_id), 1, 0
)
ON CONFLICT (workspace_id, agent_run_id) DO NOTHING;

-- name: GetUniversalDAGSegmentByVisibleAction :one
-- Resolves an idempotent visible/terminal boundary replay.
SELECT *
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND visible_action_key = sqlc.arg(visible_action_key)
  AND content_status <> 'legacy_unverified';

-- name: GetUniversalDAGSegmentByTaskGeneration :one
SELECT *
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND agent_run_id = sqlc.arg(agent_run_id)
  AND generation = sqlc.arg(generation)
  AND content_status <> 'legacy_unverified';

-- name: GetFirstUniversalDAGSegmentByTask :one
SELECT *
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND agent_run_id = sqlc.arg(agent_run_id)
  AND content_status <> 'legacy_unverified'
ORDER BY generation, segment_id
LIMIT 1;

-- name: ListUniversalDAGSegmentsByTask :many
SELECT *
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND agent_run_id = sqlc.arg(agent_run_id)
  AND content_status <> 'legacy_unverified'
ORDER BY generation, segment_id;

-- name: LockUniversalDAGSegmentForProviderCapture :one
-- Serializes finalize/replay/conflict decisions for one globally unique Segment.
SELECT *
FROM interaction_dag_segment
WHERE segment_id = sqlc.arg(segment_id)
  AND content_status <> 'legacy_unverified'
FOR UPDATE;

-- name: LockUniversalDAGEdgeIdentity :exec
-- Serializes idempotent linkage creation without requiring a schema-level key.
SELECT pg_advisory_xact_lock(hashtextextended(
  jsonb_build_array(
    sqlc.arg(workspace_id)::text,
    sqlc.arg(src_segment_id)::text,
    sqlc.arg(dst_segment_id)::text,
    sqlc.arg(edge_type)::text,
    sqlc.narg(trigger_message_id)::text
  )::text,
  454
));

-- name: GetUniversalDAGEdgeByIdentity :one
SELECT *
FROM interaction_dag_edge
WHERE workspace_id = sqlc.arg(workspace_id)
  AND src_segment_id = sqlc.arg(src_segment_id)
  AND dst_segment_id = sqlc.arg(dst_segment_id)
  AND type = sqlc.arg(edge_type)
  AND trigger_message_id IS NOT DISTINCT FROM sqlc.narg(trigger_message_id)::uuid
ORDER BY edge_seq
LIMIT 1;

-- name: LockUniversalDAGBoundaryActionKey :exec
-- Serializes the workspace-unique idempotency decision before Segment insertion.
SELECT pg_advisory_xact_lock(hashtextextended(
  jsonb_build_array(
    sqlc.arg(workspace_id)::text,
    sqlc.arg(visible_action_key)::text
  )::text,
  455
));

-- name: GetUniversalDAGSegmentProjectionFacts :one
-- Event-time facts for graph projection: everything the projector may read.
-- Current workspace/channel state is deliberately absent (Task 8: event-time
-- values only).
SELECT segment_id, workspace_id, agent_run_id, publish_status, publish_seq,
       start_seq, end_seq, close_action_kind, memory_type_at_event,
       graph_projection_eligible_at_event, derivative,
       channel_id_at_event, project_id_at_event, route_generation_at_event
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND segment_id = sqlc.arg(segment_id);

-- name: UpsertUniversalDAGSourceGuard :exec
-- The publish transaction upserts a guard row for the segment's canonical
-- task_output source (Task 8A: future publishes maintain the fence set).
INSERT INTO memory_source_guard (workspace_id, source_kind, source_id)
VALUES (sqlc.arg(workspace_id), 'task_output', sqlc.arg(source_id))
ON CONFLICT (workspace_id, source_kind, source_id) DO NOTHING;

-- name: UpsertUniversalDAGAtomProvenance :execrows
-- The publish transaction records the reverse provenance from the source to
-- every atom it produced, so retraction can quarantine the exact closure.
INSERT INTO memory_source_provenance (workspace_id, source_kind, source_id, consumer_kind, consumer_id)
SELECT sqlc.arg(workspace_id), 'task_output', sqlc.arg(source_id), 'graph_memory_atom', t.atom_id
FROM unnest(sqlc.arg(atom_ids)::text[]) AS t(atom_id)
ON CONFLICT DO NOTHING;

-- name: ListUniversalDAGTaskSourcesForRun :many
-- Canonical task_output sources that fed one Mixed-RL run (run_id linkage
-- from the Task 3B canonical adapter): the read fence for offline resolve.
SELECT DISTINCT agent_run_id
FROM interaction_dag_segment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND run_id = sqlc.arg(run_id);

-- name: ListUniversalDAGIssueSourceKeys :one
-- Task 8A issue-delete closure: every canonical memory source the issue's
-- cascade owns — the issue itself, its tasks (task_output), its comments,
-- and its attachments (issue-level and comment-level) — as "kind:id" keys
-- ready for the retraction fence.
SELECT COALESCE(array_agg(DISTINCT key), ARRAY[]::text[])::text[] AS keys
FROM (
    SELECT 'issue:' || sqlc.arg(issue_id)::uuid::text AS key
    UNION
    SELECT 'task_output:' || event.id::text
    FROM agent_inbox_event event
    WHERE event.issue_id = sqlc.arg(issue_id)
      AND event.workspace_id = sqlc.arg(workspace_id)
    UNION
    SELECT 'comment:' || c.id::text
    FROM comment c
    WHERE c.issue_id = sqlc.arg(issue_id)
      AND c.workspace_id = sqlc.arg(workspace_id)
    UNION
    SELECT 'attachment:' || a.id::text
    FROM attachment a
    WHERE a.issue_id = sqlc.arg(issue_id)
      AND a.workspace_id = sqlc.arg(workspace_id)
    UNION
    SELECT 'attachment:' || a.id::text
    FROM attachment a
    JOIN comment c ON c.id = a.comment_id
    WHERE c.issue_id = sqlc.arg(issue_id)
      AND c.workspace_id = sqlc.arg(workspace_id)
) AS sources(key);

-- name: UniversalDAGSegmentTablePresent :one
-- Read-gate probe (Task 8A): reports whether the canonical segment table
-- resolves on the current search path. to_regclass returns NULL instead of
-- erroring, so the probe is safe inside an open transaction on schemas that
-- predate migration 454.
SELECT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_name = 'interaction_dag_segment'
      AND table_schema = ANY (current_schemas(false))
) AS present;

-- name: ListSegmentTaskSources :many
-- Task 14 publication sources: the canonical task_output source of each
-- covered segment, in deterministic order for the FOR KEY SHARE lock.
SELECT segment.segment_id, segment.agent_run_id::text AS agent_run_id
FROM interaction_dag_segment segment
WHERE segment.workspace_id = sqlc.arg(workspace_id)
  AND segment.segment_id = ANY (sqlc.arg(segment_ids)::text[])
ORDER BY segment.segment_id;

-- ---------------------------------------------------------------------------
-- Training governance (spec 14.1, migration 472). Grants, the global
-- shadow/calibration switches, selection manifests, per-sample CAS states,
-- execution identity and the deletion/unlearning ledger.
-- ---------------------------------------------------------------------------

-- name: GetTrainingGovernancePolicy :one
SELECT id, selection_enabled, execution_enabled, reward_policy_version,
       per_agent_sample_cap, per_channel_sample_cap, per_workspace_sample_cap,
       updated_by, updated_at
FROM interaction_dag_training_policy
WHERE id = 1;

-- name: UpdateTrainingGovernancePolicy :one
-- Partial update of the global switches/caps; NULL fields keep their value
-- and the reward policy version is monotonic (it can only move forward).
UPDATE interaction_dag_training_policy SET
  selection_enabled = COALESCE(sqlc.narg(selection_enabled), selection_enabled),
  execution_enabled = COALESCE(sqlc.narg(execution_enabled), execution_enabled),
  reward_policy_version = GREATEST(reward_policy_version,
      COALESCE(sqlc.narg(reward_policy_version), reward_policy_version)),
  per_agent_sample_cap = COALESCE(sqlc.narg(per_agent_sample_cap), per_agent_sample_cap),
  per_channel_sample_cap = COALESCE(sqlc.narg(per_channel_sample_cap), per_channel_sample_cap),
  per_workspace_sample_cap = COALESCE(sqlc.narg(per_workspace_sample_cap), per_workspace_sample_cap),
  updated_by = sqlc.arg(updated_by),
  updated_at = now()
WHERE id = 1
RETURNING id, selection_enabled, execution_enabled, reward_policy_version,
          per_agent_sample_cap, per_channel_sample_cap, per_workspace_sample_cap,
          updated_by, updated_at;

-- name: GetTrainingGrantByWorkspace :one
SELECT * FROM interaction_dag_training_grant
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: InsertTrainingGrant :one
-- New-workspace bootstrap: the row is created with the product default the
-- caller resolved explicitly (pending_owner_ack or active), never a silent
-- database-side activation of a historical workspace.
INSERT INTO interaction_dag_training_grant (
  workspace_id, tenant_status, tenant_policy_version, tenant_granted_by, tenant_granted_at
) VALUES (
  sqlc.arg(workspace_id), sqlc.arg(tenant_status), sqlc.arg(tenant_policy_version),
  NULLIF(sqlc.arg(tenant_granted_by), ''), sqlc.narg(tenant_granted_at)
)
RETURNING *;

-- name: AckTenantTrainingGrant :execrows
-- CAS owner/admin acknowledgement: pending_owner_ack or revoked -> active,
-- bumping the policy version. A stale expected version affects zero rows.
UPDATE interaction_dag_training_grant SET
  tenant_status = 'active',
  tenant_policy_version = tenant_policy_version + 1,
  tenant_granted_by = sqlc.arg(actor),
  tenant_granted_at = now(),
  updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND tenant_status IN ('pending_owner_ack', 'revoked')
  AND tenant_policy_version = sqlc.arg(expected_version);

-- name: OptInPooledTrainingGrant :execrows
-- Pooled training is always explicit opt-in (disabled or revoked -> active).
UPDATE interaction_dag_training_grant SET
  pooled_status = 'active',
  pooled_policy_version = pooled_policy_version + 1,
  pooled_granted_by = sqlc.arg(actor),
  pooled_granted_at = now(),
  updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND pooled_status IN ('disabled', 'revoked')
  AND pooled_policy_version = sqlc.arg(expected_version);

-- name: RevokeTenantTrainingGrant :execrows
UPDATE interaction_dag_training_grant SET
  tenant_status = 'revoked', updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id) AND tenant_status = 'active';

-- name: RevokePooledTrainingGrant :execrows
UPDATE interaction_dag_training_grant SET
  pooled_status = 'revoked', updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id) AND pooled_status = 'active';

-- name: ListTrainingGrantWorkspaceBackfill :many
-- Migration 472 backfill probe: workspaces without a grant row.
SELECT w.id AS workspace_id
FROM workspace w
WHERE NOT EXISTS (
  SELECT 1 FROM interaction_dag_training_grant g WHERE g.workspace_id = w.id
);

-- name: InvalidateTrainingManifestsOnRevoke :execrows
-- A revoked grant invalidates every manifest of that purpose that has not
-- been consumed yet.
UPDATE interaction_dag_training_manifest SET
  status = 'invalidated', updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND purpose = sqlc.arg(purpose)
  AND status IN ('selected', 'exported', 'execution_started');

-- name: ListTrainingManifestIDsForPurpose :many
SELECT manifest_id
FROM interaction_dag_training_manifest
WHERE workspace_id = sqlc.arg(workspace_id)
  AND purpose = sqlc.arg(purpose);

-- name: RevokeTrainingSamplesForManifests :execrows
-- Unconsumed samples of the revoked purpose flip to revoked.
UPDATE interaction_dag_training_sample SET
  status = 'revoked', updated_at = now()
WHERE manifest_id = ANY (sqlc.arg(manifest_ids)::uuid[])
  AND status IN ('eligible', 'selected', 'exported', 'execution_started');

-- name: EnqueueTrainingDeletionLedgerRows :execrows
-- Already-consumed samples of the revoked purpose enter the deletion /
-- unlearning ledger and are excluded from later training.
INSERT INTO interaction_dag_training_deletion_ledger (
  workspace_id, sample_kind, sample_key, manifest_id, purpose, reason, requested_by
)
SELECT s.workspace_id, s.sample_kind, s.sample_key, s.manifest_id,
       sqlc.arg(purpose), sqlc.arg(reason), sqlc.arg(requested_by)
FROM interaction_dag_training_sample s
WHERE s.manifest_id = ANY (sqlc.arg(manifest_ids)::uuid[])
  AND s.status = 'consumed'
ON CONFLICT (sample_kind, sample_key, reason) DO NOTHING;

-- name: ListTrainingSegmentCandidates :many
-- Published, sanitized, non-derivative segments with an available reward,
-- excluding already-sampled segments and near-duplicate content hashes.
-- sha256/convert_to are PostgreSQL builtins, so the hash needs no extension.
SELECT s.segment_id AS item_key,
       s.workspace_id,
       COALESCE(s.sanitizer_version, '') AS sanitizer_version,
       COALESCE(s.policy_version, '') AS policy_version,
       s.project_id_at_event,
       s.channel_id_at_event,
       s.run_agent_id,
       COALESCE(s.task_id, '') AS task_id,
       s.run_id,
       encode(sha256(convert_to(s.trajectory::text, 'UTF8')), 'hex') AS item_hash,
       (SELECT count(*) FROM interaction_dag_step_reward sr
         WHERE sr.segment_id = s.segment_id) AS reward_revision
FROM interaction_dag_segment s
WHERE s.workspace_id = sqlc.arg(workspace_id)
  AND s.publish_status = 'published'
  AND s.content_status = 'published'
  AND s.trainable_eligible
  AND NOT s.derivative
  AND s.retracted_at IS NULL
  AND COALESCE(s.boundary_quality, 'exact') <> 'approximate'
  AND EXISTS (
    SELECT 1 FROM interaction_dag_step_reward sr
    WHERE sr.segment_id = s.segment_id
  )
  AND NOT EXISTS (
    SELECT 1 FROM interaction_dag_training_sample t
    WHERE t.sample_kind = 'segment'
      AND t.sample_key = s.segment_id
      AND t.status IN ('selected', 'exported', 'execution_started', 'consumed')
  )
  AND NOT EXISTS (
    SELECT 1 FROM interaction_dag_training_manifest_item i
    WHERE i.item_kind = 'segment'
      AND i.item_hash = encode(sha256(convert_to(s.trajectory::text, 'UTF8')), 'hex')
  )
ORDER BY s.published_at, s.segment_id
LIMIT sqlc.arg(limit_count);

-- name: ListTrainingSegmentSelectionAudit :many
-- Every workspace segment with its exclusion classification inputs so the
-- selection report can state WHY a segment was not selected.
SELECT segment_id, derivative, publish_status, content_status, trainable_eligible,
       (retracted_at IS NOT NULL) AS retracted,
       COALESCE(boundary_quality, 'exact') AS boundary_quality,
       EXISTS (
         SELECT 1 FROM interaction_dag_step_reward sr
         WHERE sr.segment_id = interaction_dag_segment.segment_id
       ) AS reward_available,
       EXISTS (
         SELECT 1 FROM interaction_dag_training_sample t
         WHERE t.sample_kind = 'segment'
           AND t.sample_key = interaction_dag_segment.segment_id
           AND t.status IN ('selected', 'exported', 'execution_started', 'consumed')
       ) AS already_sampled
FROM interaction_dag_segment
WHERE interaction_dag_segment.workspace_id = sqlc.arg(workspace_id)
ORDER BY segment_id;

-- name: ListTrainingGraphTrajectoryCandidates :many
-- Graph-memory explore trajectories for offline_rl recalls that are fully
-- graded and unfenced (Task 8A owner fence via memory_source_guard).
SELECT t.id::text AS item_key,
       t.workspace_id,
       t.recall_id,
       r.graph_kind,
       r.graph_owner_id,
       t.seed_index,
       encode(sha256(convert_to(
         concat_ws(':', t.id::text, t.recall_id::text, t.seed_index::text,
                   t.rounds::text, COALESCE(t.reward::text, '')),
         'UTF8')), 'hex') AS item_hash
FROM graph_memory_trajectory t
JOIN graph_memory_recall r ON r.id = t.recall_id
JOIN graph_memory_dive_job j ON j.recall_id = r.id
WHERE t.workspace_id = sqlc.arg(workspace_id)
  AND r.training_mode = 'offline_rl'
  AND t.dive_status = 'graded'
  AND j.status = 'completed'
  AND t.reward IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM memory_source_guard g
    WHERE g.workspace_id = t.workspace_id
      AND g.retracted_at IS NOT NULL
      AND g.source_kind = r.graph_kind
      AND g.source_id = r.graph_owner_id::text
  )
  AND NOT EXISTS (
    SELECT 1 FROM interaction_dag_training_sample s
    WHERE s.sample_kind = 'graph_trajectory'
      AND s.sample_key = t.id::text
      AND s.status IN ('selected', 'exported', 'execution_started', 'consumed')
  )
ORDER BY r.created_at, t.seed_index
LIMIT sqlc.arg(limit_count);

-- name: InsertTrainingManifest :one
INSERT INTO interaction_dag_training_manifest (
  workspace_id, purpose, grant_id, grant_policy_version, grant_actor, granted_at,
  workspace_config, reward_policy_version, item_count, content_hash, status
) VALUES (
  sqlc.arg(workspace_id), sqlc.arg(purpose), sqlc.arg(grant_id),
  sqlc.arg(grant_policy_version), sqlc.arg(grant_actor), sqlc.arg(granted_at),
  sqlc.arg(workspace_config), sqlc.arg(reward_policy_version),
  sqlc.arg(item_count), sqlc.arg(content_hash), 'selected'
)
RETURNING *;

-- name: InsertTrainingManifestItem :exec
INSERT INTO interaction_dag_training_manifest_item (
  manifest_id, item_kind, item_key, item_hash, sanitizer_version, policy_version,
  scope, reward_status, reward_revision
) VALUES (
  sqlc.arg(manifest_id), sqlc.arg(item_kind), sqlc.arg(item_key), sqlc.arg(item_hash),
  NULLIF(sqlc.arg(sanitizer_version), ''), NULLIF(sqlc.arg(policy_version), ''),
  sqlc.arg(scope), sqlc.arg(reward_status), sqlc.arg(reward_revision)
);

-- name: InsertTrainingSamplesEligible :execrows
-- First observation of a candidate records the eligible state; an existing
-- row of any status is left untouched (ON CONFLICT DO NOTHING).
INSERT INTO interaction_dag_training_sample (
  sample_kind, sample_key, workspace_id, status, manifest_id
)
SELECT sqlc.arg(sample_kind), e.key, sqlc.arg(workspace_id), 'eligible', sqlc.arg(manifest_id)
FROM unnest(sqlc.arg(sample_keys)::text[]) AS e(key)
ON CONFLICT (sample_kind, sample_key) DO NOTHING;

-- name: CASTrainingSamplesStateMany :execrows
-- Exactly-once per-sample transition; rows not in the expected source state
-- affect zero rows, which the caller treats as a conflict.
UPDATE interaction_dag_training_sample SET
  status = sqlc.arg(to_status),
  manifest_id = sqlc.arg(manifest_id),
  updated_at = now()
WHERE sample_kind = sqlc.arg(sample_kind)
  AND sample_key = ANY (sqlc.arg(sample_keys)::text[])
  AND status = sqlc.arg(from_status);

-- name: GetTrainingManifest :one
SELECT * FROM interaction_dag_training_manifest
WHERE manifest_id = sqlc.arg(manifest_id);

-- name: ListTrainingManifests :many
SELECT * FROM interaction_dag_training_manifest
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (sqlc.arg(purpose)::text = '' OR purpose = sqlc.arg(purpose))
ORDER BY created_at DESC
LIMIT sqlc.arg(limit_count);

-- name: ListTrainingManifestItems :many
SELECT * FROM interaction_dag_training_manifest_item
WHERE manifest_id = sqlc.arg(manifest_id)
ORDER BY item_kind, item_key;

-- name: CASTrainingManifestState :execrows
UPDATE interaction_dag_training_manifest SET
  status = sqlc.arg(to_status), updated_at = now()
WHERE manifest_id = sqlc.arg(manifest_id)
  AND status = sqlc.arg(from_status);

-- name: InsertTrainingExecution :one
INSERT INTO interaction_dag_training_execution (manifest_id, training_task_id)
VALUES (sqlc.arg(manifest_id), NULLIF(sqlc.arg(training_task_id), '')::uuid)
RETURNING *;

-- name: GetTrainingExecutionByManifest :one
SELECT * FROM interaction_dag_training_execution
WHERE manifest_id = sqlc.arg(manifest_id);

-- name: GetTrainingExecutionByTask :one
SELECT * FROM interaction_dag_training_execution
WHERE training_task_id = sqlc.arg(training_task_id);

-- name: CASConsumeTrainingExecution :execrows
UPDATE interaction_dag_training_execution SET
  status = 'consumed', consumed_at = now()
WHERE manifest_id = sqlc.arg(manifest_id)
  AND status = 'started';

-- name: CreateTrainingReplayTask :one
-- The distinct replay/training task (spec 14.1): a peer agent task carrying
-- the immutable training_execution identity in its context. Only this task
-- shape may open an AReaL session.
INSERT INTO agent_inbox_event (
  workspace_id, agent_session_id, agent_id, runtime_id, execution_config,
  issue_id, reason, requires_wake, status, priority, context
)
SELECT
  a.workspace_id, ensure_agent_wake_session(a.id), a.id, a.runtime_id,
  sqlc.arg(context), sqlc.arg(issue_id), 'training_replay', true, 'pending',
  sqlc.arg(priority), sqlc.arg(context)
FROM agent a
WHERE a.id = sqlc.arg(agent_id)
RETURNING id, workspace_id, agent_id, reason, status, context;

-- name: CountTrainingDeletionLedgerPending :one
SELECT count(*) AS pending
FROM interaction_dag_training_deletion_ledger
WHERE workspace_id = sqlc.arg(workspace_id)
  AND processed_at IS NULL;

-- name: ListTrainingDeletionLedgerRows :many
SELECT * FROM interaction_dag_training_deletion_ledger
WHERE workspace_id = sqlc.arg(workspace_id)
ORDER BY requested_at DESC
LIMIT sqlc.arg(limit_count);

-- name: StampTrainingReplayTaskExecution :execrows
-- Writes the durable execution id into the replay task's context so the
-- session-open hook can authorize against the recorded identity.
UPDATE agent_inbox_event
SET context = jsonb_set(context, '{training_execution,execution_id}', to_jsonb(sqlc.arg(execution_id)::text))
WHERE id = sqlc.arg(id);

-- name: ListTrainingGraphTrajectoryFenceAudit :many
-- Recheck-only view of previously selected graph trajectories: reward and
-- owner fence verdicts WITHOUT the already-sampled exclusion (the samples
-- are selected by design at this point).
SELECT t.id::text AS item_key,
       (t.reward IS NOT NULL) AS reward_available,
       NOT EXISTS (
         SELECT 1 FROM memory_source_guard g
         WHERE g.workspace_id = t.workspace_id
           AND g.retracted_at IS NOT NULL
           AND g.source_kind = r.graph_kind
           AND g.source_id = r.graph_owner_id::text
       ) AS unfenced
FROM graph_memory_trajectory t
JOIN graph_memory_recall r ON r.id = t.recall_id
WHERE t.workspace_id = sqlc.arg(workspace_id)
  AND t.id::text = ANY (sqlc.arg(item_keys)::text[]);

-- name: CountRealtimeDAGPublishBacklog :one
-- Realtime publish work outstanding for one workspace: pending or in-flight
-- outbox rows whose segment is a canonical (exact) boundary. The Task 22
-- legacy backfill defers entirely while any realtime work exists, so the
-- approximate channel can never consume the realtime quota (AC54).
SELECT count(*)::bigint
FROM interaction_dag_publish_outbox o
JOIN interaction_dag_segment s
  ON s.workspace_id = o.workspace_id AND s.segment_id = o.segment_id
WHERE o.workspace_id = sqlc.arg(workspace_id)
  AND o.status IN ('pending', 'processing')
  AND COALESCE(s.boundary_quality, 'exact') <> 'approximate';

-- name: ListLegacyBackfillCandidateTasks :many
-- Terminal-completed Tasks inside the backfill window that carry no Segment
-- of any kind yet. "No Segment yet" is simultaneously the existing-live-
-- Segment skip and the replay guard: once a Task has one Segment row (live
-- or backfilled) it never re-enters the candidate set.
SELECT t.id, t.workspace_id, t.issue_id, t.channel_id,
       COALESCE((SELECT MAX(m.seq) FROM task_message m WHERE m.task_id = t.id), 0)::integer AS max_seq
FROM agent_inbox_event t
WHERE t.workspace_id = sqlc.arg(workspace_id)
  AND t.status = 'acked'
  AND t.acked_at IS NOT NULL
  AND t.acked_at >= sqlc.arg(window_start)
  AND NOT EXISTS (
    SELECT 1 FROM interaction_dag_segment s
    WHERE s.workspace_id = t.workspace_id AND s.agent_run_id = t.id
  )
ORDER BY t.acked_at, t.id
LIMIT sqlc.arg(limit_count);
