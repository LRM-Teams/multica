DROP INDEX IF EXISTS idx_agent_inbox_event_runtime_pending;

ALTER TABLE agent_inbox_event
    DROP COLUMN IF EXISTS execution_config,
    DROP COLUMN IF EXISTS runtime_id;
