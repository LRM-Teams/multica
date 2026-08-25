-- Persist exact, idempotent acknowledgements for App-owned inbox sources.
CREATE TABLE agent_app_source_ack (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL,
    notification_class TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_id UUID NOT NULL,
    source_revision BIGINT NOT NULL CHECK (source_revision > 0),
    source_event_id UUID NOT NULL REFERENCES agent_reminder_occurrence(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL,
    ack_attempt_id UUID NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, app_id, notification_class, source_kind, source_id, source_revision)
);

CREATE INDEX idx_agent_app_source_ack_workspace_id ON agent_app_source_ack(workspace_id);
