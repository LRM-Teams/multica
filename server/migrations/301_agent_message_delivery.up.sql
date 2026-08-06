-- Canonical Message visibility is resolved by the Server at commit time.
-- This is deliberately not an inbox, task, lease, acknowledgement, or
-- receive-cursor table: each row only records that one Agent may receive one
-- committed channel Message. The daemon owns its local consumption boundary.
-- This migration was originally shipped as 299_agent_message_delivery before
-- being renamed during the pipeline cutover. Some existing installations have
-- the identical table recorded under the old version, so preserve that schema
-- while recording this canonical migration version.
CREATE TABLE IF NOT EXISTS agent_message_delivery (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  message_id UUID NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
  target TEXT NOT NULL,
  seq BIGINT NOT NULL CHECK (seq > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (agent_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_message_delivery_recovery
  ON agent_message_delivery(workspace_id, agent_id, target, seq, message_id);
