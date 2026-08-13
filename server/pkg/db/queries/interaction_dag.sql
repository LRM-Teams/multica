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

-- name: InsertInteractionDAGSegmentWithSnapshot :exec
-- Atomically inserts a segment row AND its 1:1 env_snapshot in a single
-- data-modifying CTE. segment_id ($1) is a text PK computed by the service
-- (<sessionID>-<trajectoryID>) and reused as the snapshot FK, so both inserts
-- commit or roll back together - a snapshot failure can never orphan the
-- segment (paired operations stay together). PostgreSQL executes the seg CTE
-- even though its result is not read by the outer INSERT, and the FK check sees
-- the seg row within the same statement. tensor_ref is the opaque tensor-ref
-- object decoded from the areal export (stored verbatim as jsonb); start_seq/
-- end_seq are the task_message.seq turn range captured at close (Task 2);
-- sandbox_ids and env_state are opaque jsonb (NOT NULL); issue_snapshot_id is
-- nullable.
WITH seg AS (
  INSERT INTO interaction_dag_segment (segment_id, project_id, agent_run_id, issue_id, task_id, trajectory_id, tensor_ref, closing_event, closing_event_target_segment, start_seq, end_seq, trajectory_source, trainable, trajectory)
  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
)
INSERT INTO interaction_dag_env_snapshot (segment_id, sandbox_ids, issue_snapshot_id, env_state)
VALUES ($1, $15, $16, $17);

-- name: GetInteractionDAGSegmentByAgentRun :one
-- Resolves a task's segment by agent_run_id (= task.ID, D8). For change 1 each
-- trained task has exactly one segment; ORDER BY created_at DESC LIMIT 1 keeps
-- this stable if a future multi-segment model adds more. Used by the
-- DELEGATION-edge recorder (D11) to find the parent's segment at the child's
-- close. Returns no rows when the parent's segment has not been recorded yet.
SELECT segment_id, project_id, agent_run_id, issue_id, task_id, trajectory_id, tensor_ref, closing_event, closing_event_target_segment, start_seq, end_seq, trajectory_source, trainable, trajectory, created_at
FROM interaction_dag_segment
WHERE agent_run_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: GetLastEndSeqForAgentRun :one
-- Returns the highest end_seq recorded for an agent_run, or 0 when no segment
-- exists yet. Used by CloseSegmentForEvent to compute the next segment's
-- start_seq (lastEnd + 1). MAX over end_seq (not "last row") so an empty [0,0]
-- segment never regresses the running start point.
SELECT COALESCE(MAX(end_seq), 0)::integer AS last_end_seq
FROM interaction_dag_segment
WHERE agent_run_id = $1;

-- name: GetInteractionDAGSegmentByID :one
-- GetInteractionDAGSegmentByID resolves a segment by its segment_id.
-- Returns pgx.ErrNoRows when no segment exists for the given ID.
SELECT segment_id, project_id, agent_run_id, issue_id, task_id, trajectory_id, tensor_ref, closing_event, closing_event_target_segment, start_seq, end_seq, trajectory_source, trainable, trajectory, created_at FROM interaction_dag_segment
WHERE segment_id = $1;

-- name: InsertInteractionDAGEdge :exec
-- Typed DAG edge. type is CHECK-constrained to delegation/mention/completion;
-- no FK to interaction_dag_segment so an edge can be recorded before both
-- endpoints are known (best-effort, validated at assembly).
INSERT INTO interaction_dag_edge (project_id, src_segment_id, dst_segment_id, type)
VALUES ($1, $2, $3, $4);

-- name: ListInteractionDAGSegmentsForProject :many
-- Read-only assembly query (U8 AssembleAssembledDag): all segments for a
-- project, ordered by created_at for deterministic assembly. SELECTs the full
-- row to scan into InteractionDAGSegment cleanly (mirrors
-- GetInteractionDAGSegmentByAgentRun), including the start_seq/end_seq turn
-- range (Task 2). No scores or message-text columns live on this table; step
-- rewards live in interaction_dag_step_reward (Task 5).
SELECT segment_id, project_id, agent_run_id, issue_id, task_id, trajectory_id, tensor_ref, closing_event, closing_event_target_segment, start_seq, end_seq, trajectory_source, trainable, trajectory, created_at
FROM interaction_dag_segment
WHERE project_id = $1
ORDER BY created_at;

-- name: ListInteractionDAGEdgesForProject :many
-- Read-only assembly query (U8): all typed edges for a project, ordered by id
-- (insertion order) for deterministic assembly.
SELECT id, project_id, src_segment_id, dst_segment_id, type, created_at
FROM interaction_dag_edge
WHERE project_id = $1
ORDER BY id;

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
WHERE s.project_id = $1;

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
WHERE s.project_id = $1
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
RETURNING *;

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

-- name: InsertMixedRLCausalEdge :one
INSERT INTO interaction_dag_causal_edge (
  edge_id, snapshot_id, run_id, src_segment_id, dst_segment_id, type,
  trigger_message_id, dst_call_id, edge_ordinal
) VALUES (
  sqlc.arg(edge_id), NULLIF(sqlc.arg(snapshot_id), ''), sqlc.arg(run_id),
  sqlc.arg(src_segment_id), sqlc.arg(dst_segment_id), sqlc.arg(type),
  sqlc.narg(trigger_message_id), NULLIF(sqlc.arg(dst_call_id), ''),
  sqlc.arg(edge_ordinal)
)
RETURNING *;

-- name: ListMixedRLCausalEdgesCanonical :many
SELECT * FROM interaction_dag_causal_edge
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
SELECT * FROM interaction_dag_run_segment
WHERE snapshot_id = sqlc.arg(snapshot_id)
ORDER BY segment_ordinal, segment_id;

-- name: ListMixedRLRunSegmentsCanonical :many
-- Provisional plus terminal segments in the deterministic order used by the
-- freeze manifest, before snapshot_id is assigned.
SELECT * FROM interaction_dag_run_segment
WHERE run_id = sqlc.arg(run_id)
ORDER BY segment_ordinal, segment_id;

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
