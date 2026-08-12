-- Existing deliveries predate the ACK contract and were governed by recovery
-- snapshots. Mark that population as legacy-settled, then require ACKs for all
-- future inserts without guessing a deployment timestamp.
ALTER TABLE agent_message_delivery
  ADD COLUMN ack_required boolean NOT NULL DEFAULT false;

ALTER TABLE agent_message_delivery
  ALTER COLUMN ack_required SET DEFAULT true;

DROP INDEX IF EXISTS idx_agent_message_delivery_unacked;
CREATE INDEX idx_agent_message_delivery_unacked
  ON agent_message_delivery(workspace_id, agent_id, seq, message_id)
  WHERE ack_required AND acked_at IS NULL;
