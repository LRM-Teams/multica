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
SELECT run_id, project_id, task_id, topology_hash, ordered_segment_ids, status, current_segment_ordinal, pi_session_id, last_error, created_at, updated_at, completed_at, sandbox_instance_id, capability_token_hash, execution_mode
FROM interaction_dag_diagnosis_run
WHERE run_id = $1;

-- name: GetResumableInteractionDAGDiagnosisRun :one
-- Latest still-active (provisioning/running/compacting) run for a
-- (project, task); used to resume an interrupted diagnosis instead of
-- starting over. 'provisioning' is included so a sandbox-mode run whose
-- server crashed mid-provisioning can be resumed or re-provisioned.
SELECT run_id, project_id, task_id, topology_hash, ordered_segment_ids, status, current_segment_ordinal, pi_session_id, last_error, created_at, updated_at, completed_at, sandbox_instance_id, capability_token_hash, execution_mode
FROM interaction_dag_diagnosis_run
WHERE project_id = $1 AND task_id = $2 AND status IN ('provisioning', 'running', 'compacting')
ORDER BY updated_at DESC
LIMIT 1;

-- name: GetLatestCompletedInteractionDAGDiagnosisRun :one
-- Used by idempotent on-demand requests: a completed diagnosis for the exact
-- same terminal DAG is returned rather than launching another Pi session.
SELECT run_id, project_id, task_id, topology_hash, ordered_segment_ids, status, current_segment_ordinal, pi_session_id, last_error, created_at, updated_at, completed_at, sandbox_instance_id, capability_token_hash, execution_mode
FROM interaction_dag_diagnosis_run
WHERE project_id = $1 AND task_id = $2 AND status = 'completed'
ORDER BY completed_at DESC, updated_at DESC
LIMIT 1;

-- name: GetLatestInteractionDAGDiagnosisRunForProject :one
-- Latest diagnosis run of any status for a project; backs the human-facing
-- /diagnosis/latest polling endpoint.
SELECT run_id, project_id, task_id, topology_hash, ordered_segment_ids, status, current_segment_ordinal, pi_session_id, last_error, created_at, updated_at, completed_at, sandbox_instance_id, capability_token_hash, execution_mode
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
SET sandbox_instance_id = $2, capability_token_hash = $3, execution_mode = $4, updated_at = now()
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
