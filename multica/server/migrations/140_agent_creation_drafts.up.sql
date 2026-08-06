CREATE TABLE agent_creation_draft (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    created_by_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    target_user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    instructions TEXT NOT NULL DEFAULT '',
    avatar_url TEXT,
    visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('workspace', 'private')),
    project_id UUID REFERENCES project(id) ON DELETE SET NULL,
    channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
    can_execute_code BOOLEAN NOT NULL DEFAULT false,
    suggested_channels JSONB NOT NULL DEFAULT '[]'::jsonb,
    recommended_tools JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'used', 'dismissed')),
    used_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at TIMESTAMPTZ
);

CREATE INDEX idx_agent_creation_draft_target
    ON agent_creation_draft(workspace_id, target_user_id, status, created_at DESC);

CREATE INDEX idx_agent_creation_draft_creator
    ON agent_creation_draft(created_by_agent_id)
    WHERE created_by_agent_id IS NOT NULL;
