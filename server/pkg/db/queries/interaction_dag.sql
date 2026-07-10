-- name: UpsertInteractionDAGSessionRun :exec
-- Idempotent on session_id: a retry that re-opens a session re-binds it to the
-- latest agent_run_id (= task.ID, D8) + issue_id. agent_run_id is the multica
-- agent_task_queue PK (attempt-level), NOT the agent UUID.
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
  INSERT INTO interaction_dag_segment (segment_id, project_id, agent_run_id, issue_id, task_id, trajectory_id, tensor_ref, closing_event, closing_event_target_segment, start_seq, end_seq)
  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
)
INSERT INTO interaction_dag_env_snapshot (segment_id, sandbox_ids, issue_snapshot_id, env_state)
VALUES ($1, $12, $13, $14);

-- name: GetInteractionDAGSegmentByAgentRun :one
-- Resolves a task's segment by agent_run_id (= task.ID, D8). For change 1 each
-- trained task has exactly one segment; ORDER BY created_at DESC LIMIT 1 keeps
-- this stable if a future multi-segment model adds more. Used by the
-- DELEGATION-edge recorder (D11) to find the parent's segment at the child's
-- close. Returns no rows when the parent's segment has not been recorded yet.
SELECT segment_id, project_id, agent_run_id, issue_id, task_id, trajectory_id, tensor_ref, closing_event, closing_event_target_segment, start_seq, end_seq, created_at
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
SELECT segment_id, project_id, agent_run_id, issue_id, task_id, trajectory_id, tensor_ref, closing_event, closing_event_target_segment, start_seq, end_seq, created_at
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
