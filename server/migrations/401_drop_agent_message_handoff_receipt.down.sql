CREATE TABLE agent_message_handoff_receipt (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    handoff_id TEXT NOT NULL CHECK (length(btrim(handoff_id)) BETWEEN 1 AND 200),
    message_count INTEGER NOT NULL CHECK (message_count > 0),
    targets JSONB NOT NULL DEFAULT '[]'::jsonb,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, agent_id, handoff_id)
);

CREATE INDEX agent_message_handoff_receipt_agent_id_idx
  ON agent_message_handoff_receipt (agent_id);
