ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention',
    'dm',
    'ambient',
    'thread_reply',
    'channel_message',
    'attention_response_grant',
    'attention_convergence',
    'attention_manager_fallback',
    'collaboration_turn'
  ));
