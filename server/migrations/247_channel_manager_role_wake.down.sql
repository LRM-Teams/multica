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

-- The older schema has no dedicated role-change reason. Refuse the rollback
-- while these durable rows exist: silently remapping them would erase the
-- reason that an operator needs to reconcile before a downgrade.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM agent_inbox_event WHERE reason = 'channel_role_changed') THEN
    RAISE EXCEPTION 'cannot roll back migration 247 while channel_role_changed wake rows exist';
  END IF;
END;
$$;

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
