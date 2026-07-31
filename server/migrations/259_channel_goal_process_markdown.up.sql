-- Per-manager process Markdown for Adaptive Channel Goal Mode v2 (LRM-931).
-- One long-form document per channel manager agent under the channel's current
-- goal. Distinct from progress_summary / current_step / blocker (short status).
CREATE TABLE channel_goal_process_markdown (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    goal_id UUID NOT NULL REFERENCES channel_goal(id) ON DELETE CASCADE,
    manager_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    content TEXT NOT NULL DEFAULT '' CHECK (octet_length(content) <= 200000),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_by_type TEXT NOT NULL CHECK (updated_by_type IN ('user', 'agent')),
    updated_by_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT channel_goal_process_markdown_unique_manager
        UNIQUE (goal_id, manager_agent_id)
);

CREATE INDEX channel_goal_process_markdown_goal_idx
    ON channel_goal_process_markdown(goal_id, updated_at DESC);

CREATE INDEX channel_goal_process_markdown_channel_idx
    ON channel_goal_process_markdown(workspace_id, channel_id);

-- Supporting index for agent(id) ON DELETE CASCADE (agent hard-delete scans).
CREATE INDEX channel_goal_process_markdown_manager_agent_idx
    ON channel_goal_process_markdown(manager_agent_id);
