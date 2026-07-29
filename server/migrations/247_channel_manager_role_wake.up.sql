BEGIN;

-- channel_member(agent, manager) is the only live group-manager identity.
-- Legacy singleton bindings are migrated only when the bound agent is already
-- a real member of the same channel; the old provisioning path always created
-- that membership. Fail closed instead of manufacturing a membership (and an
-- onboarding wake) during deploy.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM channel channel_row
    JOIN agent manager
      ON manager.id = channel_row.group_manager_agent_id
     AND manager.workspace_id = channel_row.workspace_id
    WHERE channel_row.group_manager_agent_id IS NOT NULL
      AND NOT EXISTS (
        SELECT 1
        FROM channel_member membership
        WHERE membership.workspace_id = channel_row.workspace_id
          AND membership.channel_id = channel_row.id
          AND membership.member_type = 'agent'
          AND membership.member_id = manager.id
      )
  ) THEN
    RAISE EXCEPTION 'legacy group manager binding has no same-channel membership'
      USING ERRCODE = 'check_violation';
  END IF;
END;
$$;

UPDATE channel_member membership
SET role = 'manager'
FROM channel channel_row
JOIN agent manager
  ON manager.id = channel_row.group_manager_agent_id
 AND manager.workspace_id = channel_row.workspace_id
WHERE channel_row.group_manager_agent_id IS NOT NULL
  AND membership.workspace_id = channel_row.workspace_id
  AND membership.channel_id = channel_row.id
  AND membership.member_type = 'agent'
  AND membership.member_id = manager.id
  AND membership.role <> 'manager';

-- The replacement wake is a normal durable agent inbox reason.
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

-- Retire the server-owned patrol identity. These reminders were an
-- implementation detail of the removed per-channel Beckham model, so keeping
-- them visible as ordinary reminders would be misleading.
DELETE FROM agent_reminder
WHERE origin_kind = 'group_manager_auto'
   OR managed_kind = 'patrol';

DROP INDEX IF EXISTS idx_agent_reminder_group_manager_dormant_patrol;

-- No new ambient watches are written after the application cutover. Remove
-- queued singleton-manager work so an old Beckham patrol cannot drain later.
DELETE FROM wendy_channel_ambient;

-- Keep managed_role for the independent research_fleet feature, but make the
-- retired group_manager value impossible to write again.
UPDATE agent
SET managed_role = NULL
WHERE managed_role = 'group_manager';

ALTER TABLE agent
  DROP CONSTRAINT IF EXISTS agent_managed_role_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_managed_role_check
  CHECK (managed_role IS NULL OR managed_role = 'research_fleet');

COMMIT;
