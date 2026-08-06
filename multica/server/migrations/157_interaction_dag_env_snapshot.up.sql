-- 157_interaction_dag_env_snapshot: 1:1 env snapshot per segment (sandbox ids,
-- issue snapshot, env state). Separate table so the segment row stays lean;
-- cascades with its segment.
CREATE TABLE IF NOT EXISTS interaction_dag_env_snapshot (
    segment_id text PRIMARY KEY REFERENCES interaction_dag_segment(segment_id) ON DELETE CASCADE,
    sandbox_ids jsonb NOT NULL,
    issue_snapshot_id text,
    env_state jsonb NOT NULL DEFAULT '{}'::jsonb
);
