ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_runtime_id_fkey;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_runtime_id_fkey
  FOREIGN KEY (runtime_id) REFERENCES agent_runtime(id);
