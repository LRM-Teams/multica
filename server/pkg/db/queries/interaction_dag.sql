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

-- name: InsertInteractionDAGSegment :exec
-- One row per closed communication-bounded segment. tensor_ref is the opaque
-- tensor-ref object decoded from the areal export (stored verbatim as jsonb).
INSERT INTO interaction_dag_segment (segment_id, project_id, agent_run_id, issue_id, task_id, trajectory_id, tensor_ref, closing_event, closing_event_target_segment)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: InsertInteractionDAGEnvSnapshot :exec
-- 1:1 env snapshot per segment (cascades with its segment). sandbox_ids and
-- env_state are opaque jsonb; issue_snapshot_id is nullable traceability.
INSERT INTO interaction_dag_env_snapshot (segment_id, sandbox_ids, issue_snapshot_id, env_state)
VALUES ($1, $2, $3, $4);

-- name: InsertInteractionDAGEdge :exec
-- Typed DAG edge. type is CHECK-constrained to delegation/mention/completion;
-- no FK to interaction_dag_segment so an edge can be recorded before both
-- endpoints are known (best-effort, validated at assembly).
INSERT INTO interaction_dag_edge (project_id, src_segment_id, dst_segment_id, type)
VALUES ($1, $2, $3, $4);
