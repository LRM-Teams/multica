-- Agent-bound memories synced from runtime-local per-agent caches.
-- Records are scoped to agent_id instead of runtime_id so memories survive
-- moving an agent between runtimes.

CREATE TABLE agent_memory (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
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

CREATE INDEX idx_agent_memory_workspace ON agent_memory(workspace_id);
CREATE INDEX idx_agent_memory_agent ON agent_memory(agent_id);
CREATE INDEX idx_agent_memory_sync ON agent_memory(agent_id, sync_key);
