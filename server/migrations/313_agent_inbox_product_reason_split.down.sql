-- Revert product reason split. Rows already written with chat_session /
-- voice_call / issue_thread_backflow must be rewritten first.
UPDATE agent_inbox_event
SET reason = 'dm'
WHERE reason IN ('chat_session', 'voice_call');

UPDATE agent_inbox_event
SET reason = 'mention'
WHERE reason = 'issue_thread_backflow';

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
    'collaboration_turn',
    'collaboration_manager_fallback',
    'channel_onboarding',
    'issue',
    'quick_create',
    'autopilot',
    'agent_radar',
    'training',
    'environment_dispatch',
    'memory_curation',
    'reminder',
    'channel_role_changed'
  ));
