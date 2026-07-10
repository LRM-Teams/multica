-- Add start_seq and end_seq columns to interaction_dag_segment to track per-segment turn ranges.
ALTER TABLE interaction_dag_segment
    ADD COLUMN start_seq INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN end_seq INTEGER NOT NULL DEFAULT 0;

-- Create interaction_dag_step_reward table to store per-step judge rewards.
CREATE TABLE interaction_dag_step_reward (
    segment_id TEXT NOT NULL REFERENCES interaction_dag_segment(segment_id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    score INTEGER NOT NULL CHECK (score >= 0),
    rationale TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (segment_id, seq)
);
