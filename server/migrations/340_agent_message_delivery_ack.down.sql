DROP INDEX IF EXISTS idx_agent_message_delivery_unacked;

ALTER TABLE agent_message_delivery
  DROP COLUMN IF EXISTS acked_at;
