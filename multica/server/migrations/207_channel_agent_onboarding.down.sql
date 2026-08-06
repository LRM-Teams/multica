BEGIN;

-- Task #101 (2026-08-02): this down migration used to silently DELETE any
-- agent_inbox_event row (and its associated chat_message rows) with
-- reason='channel_onboarding' before tearing down the feature that creates
-- them. Those are real work items — an agent that was queued to receive a
-- channel-join notification simply never gets it, with no warning. There is
-- no safe remap target (no other 'reason' value means the same thing), and
-- this table is agent-facing work queue state, not disposable cache. Fail
-- loud instead, before any of the structural teardown below runs, matching
-- migration 107/143/181/182/186's fix (task #99/#101).
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM agent_inbox_event WHERE reason = 'channel_onboarding';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 207 down cannot proceed: % row(s) in agent_inbox_event have reason=''channel_onboarding''. There is no safe value to remap them to, and this migration must not silently delete agent work-queue items. If you accept permanently losing these inbox events (and their linked chat_message rows), run: DELETE FROM chat_message WHERE task_id IN (SELECT id FROM agent_inbox_event WHERE reason = ''channel_onboarding''); DELETE FROM agent_inbox_event WHERE reason = ''channel_onboarding''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

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
