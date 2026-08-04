-- Human-confirmable action cards prepared by agents (Raft-aligned agent:create hire).
-- No Multica agent_creation_draft bridge: FE binds CreateAgentDialog to card id.

CREATE TABLE agent_action_card (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    action_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'prepared'
        CHECK (status IN ('prepared', 'done', 'dismissed')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    prepared_by_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
    committed_by_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    committed_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    done_at TIMESTAMPTZ,
    CONSTRAINT agent_action_card_type_check CHECK (action_type IN ('agent:create'))
);

CREATE INDEX idx_agent_action_card_workspace_status
    ON agent_action_card(workspace_id, status, created_at DESC);

CREATE INDEX idx_agent_action_card_prepared_by
    ON agent_action_card(prepared_by_agent_id)
    WHERE prepared_by_agent_id IS NOT NULL;
