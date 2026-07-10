-- 155_interaction_dag_segment: one row per closed communication-bounded segment.
-- Mirrors the AssembledDag SegmentSpec shape (structure only: no scores,
-- turn-idx, or message text cross this boundary).
CREATE TABLE IF NOT EXISTS interaction_dag_segment (
    segment_id text PRIMARY KEY,
    project_id text NOT NULL,
    agent_run_id text NOT NULL,
    issue_id text,
    task_id text,
    trajectory_id bigint NOT NULL,
    tensor_ref jsonb NOT NULL,
    closing_event text,
    closing_event_target_segment text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_interaction_dag_segment_project
    ON interaction_dag_segment (project_id);
