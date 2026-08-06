ALTER TABLE workspace
    ADD COLUMN onboarding_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL;

CREATE INDEX idx_workspace_onboarding_agent_id
    ON workspace (onboarding_agent_id)
    WHERE onboarding_agent_id IS NOT NULL;
