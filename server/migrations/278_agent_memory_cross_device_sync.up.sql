CREATE SEQUENCE IF NOT EXISTS agent_memory_sync_change_seq;

ALTER TABLE agent_memory_sync_entry
  ADD COLUMN change_seq BIGINT NOT NULL DEFAULT nextval('agent_memory_sync_change_seq'),
  ADD COLUMN deleted_at TIMESTAMPTZ;

ALTER SEQUENCE agent_memory_sync_change_seq
  OWNED BY agent_memory_sync_entry.change_seq;

CREATE INDEX idx_agent_memory_sync_entry_agent_change
  ON agent_memory_sync_entry (agent_id, change_seq, id);
