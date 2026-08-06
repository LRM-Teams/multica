-- Agent-bound shared skills synced from runtime-local per-agent caches.

CREATE TABLE agent_shared_skill (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}',
    sync_key TEXT NOT NULL,
    content_hash TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(agent_id, name),
    UNIQUE(agent_id, sync_key)
);

CREATE TABLE agent_shared_skill_file (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_shared_skill_id UUID NOT NULL REFERENCES agent_shared_skill(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(agent_shared_skill_id, path)
);

CREATE INDEX idx_agent_shared_skill_workspace ON agent_shared_skill(workspace_id);
CREATE INDEX idx_agent_shared_skill_agent ON agent_shared_skill(agent_id);
CREATE INDEX idx_agent_shared_skill_sync ON agent_shared_skill(agent_id, sync_key);
CREATE INDEX idx_agent_shared_skill_file_skill ON agent_shared_skill_file(agent_shared_skill_id);
CREATE INDEX idx_agent_shared_skill_file_agent ON agent_shared_skill_file(agent_id);
