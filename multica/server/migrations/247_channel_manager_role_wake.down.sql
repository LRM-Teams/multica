BEGIN;

-- This rollback restores schema acceptance only. It cannot reconstruct deleted
-- server-owned patrol reminders or the retired per-channel Beckham bindings.
ALTER TABLE channel ADD COLUMN group_manager_agent_id UUID;
CREATE INDEX idx_channel_group_manager_agent ON channel (group_manager_agent_id);

ALTER TABLE agent
  DROP CONSTRAINT IF EXISTS agent_managed_role_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_managed_role_check
  CHECK (managed_role IS NULL OR managed_role IN ('group_manager', 'research_fleet'));

-- Task #100 (2026-08-02): 'channel_role_changed' is its own durable wake
-- reason (up.sql's comment: "The replacement wake is a normal durable agent
-- inbox reason") with no equivalent among the remaining values — every other
-- reason here is triggered by chat content (mention/dm/thread_reply/...),
-- not a membership/role change. There is no safe remap target. Fail loud
-- instead of letting ALTER TABLE...ADD CONSTRAINT bounce off a raw Postgres
-- constraint-violation error, matching migrations 107/143/181/182/186/207/
-- 254/268's fix (tasks #99/#101).
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count
      FROM agent_inbox_event WHERE reason = 'channel_role_changed';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 247 down cannot proceed: % row(s) in agent_inbox_event have reason=''channel_role_changed''. There is no safe value to remap them to — every other reason value is triggered by chat content, not a membership/role change. If you accept permanently losing these wake events, run: DELETE FROM agent_inbox_event WHERE reason = ''channel_role_changed''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

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
    'reminder'
  ));

COMMIT;
