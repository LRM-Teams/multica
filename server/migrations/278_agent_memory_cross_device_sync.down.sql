DROP INDEX IF EXISTS idx_agent_memory_sync_entry_agent_change;

ALTER TABLE agent_memory_sync_entry
  DROP COLUMN IF EXISTS deleted_at,
  DROP COLUMN IF EXISTS change_seq;

DROP SEQUENCE IF EXISTS agent_memory_sync_change_seq;
