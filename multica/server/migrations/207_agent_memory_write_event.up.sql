-- Append-only log of agent-local memory file writes (Phase ① feedback).
-- Distinct from agent_memory (curated content sync).

CREATE TABLE agent_memory_write_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    rel_path TEXT NOT NULL,
    scope_type TEXT NOT NULL CHECK (scope_type IN ('agent_global', 'agent_state', 'user', 'channel', 'project')),
    file_key TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    delta_chars INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_memory_write_event_agent_created
    ON agent_memory_write_event (agent_id, created_at DESC);

CREATE INDEX idx_agent_memory_write_event_agent_path_created
    ON agent_memory_write_event (agent_id, rel_path, created_at DESC);

CREATE TABLE agent_memory_write_daily (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    write_date DATE NOT NULL,
    write_count INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, write_date)
);
