-- Durable first-start intent for an Agent created through the human-managed
-- provisioning boundary. The dispatch id is stable across every retry so a
-- Computer can safely deduplicate an acknowledgement whose response was lost.
CREATE TABLE agent_start_intent (
    start_dispatch_id UUID PRIMARY KEY,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'accepted', 'queued', 'ready', 'failed')),
    dispatch_attempts INTEGER NOT NULL DEFAULT 0 CHECK (dispatch_attempts >= 0),
    last_dispatched_at TIMESTAMPTZ,
    accepted_at TIMESTAMPTZ,
    ready_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    failure_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id)
);

CREATE INDEX agent_start_intent_pending_runtime_idx
    ON agent_start_intent (runtime_id, created_at, start_dispatch_id)
    WHERE status = 'pending';
