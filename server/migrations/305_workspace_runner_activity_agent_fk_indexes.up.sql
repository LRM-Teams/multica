-- Agent deletion follows foreign keys by agent_id. These indexes keep the
-- Workspace Runner Activity and canonical Message handoff tables from turning
-- a hard delete into a full-table scan.
CREATE INDEX IF NOT EXISTS agent_activity_entry_agent_id_idx
  ON agent_activity_entry (agent_id);

CREATE INDEX IF NOT EXISTS agent_activity_launch_agent_id_idx
  ON agent_activity_launch (agent_id);

CREATE INDEX IF NOT EXISTS agent_activity_probe_agent_id_idx
  ON agent_activity_probe (agent_id);

CREATE INDEX IF NOT EXISTS agent_activity_snapshot_agent_id_idx
  ON agent_activity_snapshot (agent_id);

CREATE INDEX IF NOT EXISTS agent_message_handoff_receipt_agent_id_idx
  ON agent_message_handoff_receipt (agent_id);
