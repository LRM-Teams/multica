-- Durable at-least-once ledger for Standalone Agent Chat (FAB / bubble).
-- chat_message is the conversation fact; this row is only the Computer
-- transfer. It is not an inbox event, task, or channel Message.
CREATE TABLE agent_chat_delivery (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  chat_session_id UUID NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
  message_id UUID NOT NULL REFERENCES chat_message(id) ON DELETE CASCADE,
  target TEXT NOT NULL,
  seq BIGINT NOT NULL CHECK (seq > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  acked_at TIMESTAMPTZ,
  PRIMARY KEY (agent_id, message_id)
);

CREATE INDEX idx_agent_chat_delivery_unacked
  ON agent_chat_delivery(workspace_id, agent_id, seq, message_id)
  WHERE acked_at IS NULL;
