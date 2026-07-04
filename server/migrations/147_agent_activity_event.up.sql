CREATE TABLE agent_activity_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    event_kind TEXT NOT NULL CHECK (event_kind IN ('lifecycle', 'platform_decision')),
    event_type TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'info' CHECK (severity IN ('info', 'warning', 'error')),
    target_kind TEXT NOT NULL DEFAULT 'none' CHECK (target_kind IN ('issue', 'dm', 'channel', 'thread', 'agent', 'none')),
    target_id UUID,
    target_slug TEXT,
    reason_code TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_activity_event_workspace_agent_created
    ON agent_activity_event (workspace_id, agent_id, created_at DESC, id DESC);

CREATE INDEX idx_agent_activity_event_task
    ON agent_activity_event (task_id)
    WHERE task_id IS NOT NULL;

CREATE INDEX idx_agent_activity_event_runtime
    ON agent_activity_event (workspace_id, runtime_id, created_at DESC)
    WHERE runtime_id IS NOT NULL;
