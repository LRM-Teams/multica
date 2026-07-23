BEGIN;

DROP TRIGGER IF EXISTS trg_maintain_channel_agent_onboarding ON channel_member;
DROP FUNCTION IF EXISTS maintain_channel_agent_onboarding();

DELETE FROM chat_message
WHERE task_id IN (
  SELECT id FROM agent_inbox_event WHERE reason = 'channel_onboarding'
);

DELETE FROM agent_inbox_event
WHERE reason = 'channel_onboarding';

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_channel_onboarding_shape_check,
  DROP COLUMN IF EXISTS channel_onboarding_id;

DROP TABLE IF EXISTS channel_agent_onboarding;

DROP INDEX IF EXISTS channel_message_membership_generation_unique;
ALTER TABLE channel_message
  DROP CONSTRAINT IF EXISTS channel_message_membership_generation_shape_check,
  DROP COLUMN IF EXISTS membership_generation_id;

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
    'collaboration_manager_fallback'
  ));

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_terminal_outcome_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_terminal_outcome_check
  CHECK (terminal_outcome IN ('replied', 'no_reply', 'held', 'failed'));

DROP INDEX IF EXISTS channel_member_generation_unique;
ALTER TABLE channel_member
  DROP CONSTRAINT IF EXISTS channel_member_join_source_check,
  DROP COLUMN IF EXISTS join_source,
  DROP COLUMN IF EXISTS added_by,
  DROP COLUMN IF EXISTS generation_id;

COMMIT;
