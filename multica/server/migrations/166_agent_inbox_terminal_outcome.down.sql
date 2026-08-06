DROP INDEX IF EXISTS idx_agent_inbox_event_terminal_retry;

ALTER TABLE agent_inbox_event
  DROP COLUMN IF EXISTS terminal_at,
  DROP COLUMN IF EXISTS retryable,
  DROP COLUMN IF EXISTS terminal_delivery_id,
  DROP COLUMN IF EXISTS terminal_outcome;
