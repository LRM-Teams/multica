-- 156_interaction_dag_edge: typed DAG edges between segments.
-- type is constrained to the three EdgeType values (delegation/mention/
-- completion). No FK to interaction_dag_segment so edges can be recorded
-- before both endpoints are known (best-effort); assembly validates integrity.
CREATE TABLE IF NOT EXISTS interaction_dag_edge (
    id bigserial PRIMARY KEY,
    project_id text NOT NULL,
    src_segment_id text NOT NULL,
    dst_segment_id text NOT NULL,
    type text NOT NULL CHECK (type IN ('delegation', 'mention', 'completion')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_interaction_dag_edge_project
    ON interaction_dag_edge (project_id);
