DROP INDEX IF EXISTS idx_agent_task_transport_audit_inbox_event;

ALTER TABLE agent_task_transport_audit
  DROP CONSTRAINT IF EXISTS agent_task_transport_audit_source_check;

ALTER TABLE agent_task_transport_audit
  DROP COLUMN IF EXISTS inbox_event_id;

DELETE FROM agent_task_transport_audit WHERE task_id IS NULL;

ALTER TABLE agent_task_transport_audit
  ALTER COLUMN task_id SET NOT NULL;

DROP INDEX IF EXISTS idx_agent_inbox_token_event;
DROP INDEX IF EXISTS idx_agent_inbox_token_hash;
DROP TABLE IF EXISTS agent_inbox_token;
