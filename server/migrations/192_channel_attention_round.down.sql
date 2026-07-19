-- Stop any restricted work before restoring the pre-PR-2 ambient uniqueness
-- contract. Migrations are run while the server is offline, so no new lease can
-- race this rollback.
UPDATE agent_event_delivery delivery
SET status = 'expired',
    last_error = 'channel attention feature rolled back',
    updated_at = now()
FROM agent_inbox_event event
WHERE delivery.inbox_event_id = event.id
  AND event.delivery_mode = 'attention'
  AND delivery.status IN ('leased', 'processing');

UPDATE agent_inbox_event
SET status = 'suppressed',
    last_error = 'channel attention feature rolled back',
    updated_at = now()
WHERE delivery_mode = 'attention'
  AND status IN ('pending', 'draining', 'failed');

DROP TABLE IF EXISTS channel_attention_dispatch_outbox;
DROP TABLE IF EXISTS channel_attention_participant;
DROP TABLE IF EXISTS channel_attention_round;

DROP INDEX IF EXISTS idx_agent_inbox_event_ambient_pending_unique;

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_response_mode_check,
  DROP CONSTRAINT IF EXISTS agent_inbox_event_delivery_mode_check,
  DROP COLUMN IF EXISTS response_mode,
  DROP COLUMN IF EXISTS delivery_mode;

CREATE UNIQUE INDEX idx_agent_inbox_event_ambient_pending_unique
  ON agent_inbox_event(conversation_id, agent_id)
  WHERE reason = 'ambient'
    AND status IN ('pending', 'failed')
    AND conversation_id IS NOT NULL;
