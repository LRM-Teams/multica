DROP TABLE daemon_heartbeat;

-- Recreates daemon_connection's final shape (001_init + 003_task_context)
-- for symmetry. It was unused before this migration and stays unused after
-- a rollback; this only restores the schema, not any behavior.
CREATE TABLE daemon_connection (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    daemon_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'disconnected'
        CHECK (status IN ('connected', 'disconnected')),
    last_heartbeat_at TIMESTAMPTZ,
    runtime_info JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_daemon_agent UNIQUE (agent_id, daemon_id)
);
