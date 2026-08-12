ALTER TABLE agent_message_delivery
  ADD COLUMN IF NOT EXISTS acked_at timestamptz;

CREATE INDEX IF NOT EXISTS idx_agent_message_delivery_unacked
  ON agent_message_delivery(workspace_id, agent_id, seq, message_id)
  WHERE acked_at IS NULL;
