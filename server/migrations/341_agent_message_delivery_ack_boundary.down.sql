DROP INDEX IF EXISTS idx_agent_message_delivery_unacked;

ALTER TABLE agent_message_delivery
  DROP COLUMN IF EXISTS ack_required;

CREATE INDEX IF NOT EXISTS idx_agent_message_delivery_unacked
  ON agent_message_delivery(workspace_id, agent_id, seq, message_id)
  WHERE acked_at IS NULL;
