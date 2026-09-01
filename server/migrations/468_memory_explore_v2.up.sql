-- Task 10: structured MemoryRef surface + the Explore plan ledger (spec
-- §8.3/§10). One persisted plan per (workspace, trajectory): the pinned
-- graphs, the frozen watermarks the plan is allowed to read up to, and the
-- server-side budgets. Rows are written only while the memory_explore_v2
-- route is green (Task 8A phase gate); a disabled route never persists a
-- user/Agent trajectory.

BEGIN;

CREATE TABLE memory_explore_plan (
    workspace_id               uuid NOT NULL,
    trajectory_id              text NOT NULL,
    pinned_graphs              jsonb NOT NULL DEFAULT '[]'::jsonb,
    segment_publish_seq_max    bigint NOT NULL DEFAULT 0,
    interaction_edge_seq_max   bigint NOT NULL DEFAULT 0,
    budgets                    jsonb NOT NULL DEFAULT '{}'::jsonb,
    rollover_count             integer NOT NULL DEFAULT 0,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, trajectory_id),
    CONSTRAINT memory_explore_plan_trajectory_shape_check CHECK (
        length(btrim(trajectory_id)) BETWEEN 1 AND 128
    ),
    CONSTRAINT memory_explore_plan_graphs_shape_check CHECK (
        jsonb_typeof(pinned_graphs) = 'array'
    ),
    CONSTRAINT memory_explore_plan_watermark_shape_check CHECK (
        segment_publish_seq_max >= 0 AND interaction_edge_seq_max >= 0
    )
);

CREATE INDEX memory_explore_plan_workspace_idx
    ON memory_explore_plan (workspace_id, updated_at);

COMMIT;
