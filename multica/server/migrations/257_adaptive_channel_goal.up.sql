CREATE TABLE channel_goal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 160),
    objective TEXT NOT NULL CHECK (length(btrim(objective)) BETWEEN 1 AND 8000),
    success_criteria JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(success_criteria) = 'array' AND jsonb_array_length(success_criteria) BETWEEN 1 AND 50),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'paused', 'completed', 'cancelled')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    progress_summary TEXT NOT NULL DEFAULT '' CHECK (length(progress_summary) <= 8000),
    current_step TEXT NOT NULL DEFAULT '' CHECK (length(current_step) <= 1000),
    blocker TEXT NOT NULL DEFAULT '' CHECK (length(blocker) <= 4000),
    evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(evidence_refs) = 'array'),
    completed_criteria JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(completed_criteria) = 'array'),
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('user', 'agent')),
    created_by_id UUID NOT NULL,
    updated_by_type TEXT NOT NULL CHECK (updated_by_type IN ('user', 'agent')),
    updated_by_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX channel_goal_one_current
    ON channel_goal(channel_id)
    WHERE status IN ('active', 'paused');

CREATE INDEX channel_goal_channel_history
    ON channel_goal(channel_id, created_at DESC);
