UPDATE agent_inbox_event
SET reason = 'ambient'
WHERE reason = 'thread_reply';

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN ('mention', 'dm', 'ambient'));
