BEGIN;

-- channel_member(agent, manager) is the only live group-manager identity.
-- Migrate a legacy singleton binding only when the bound agent is still a real
-- member of the same channel. Orphan bindings are discarded with the retired
-- column instead of manufacturing membership or an onboarding wake.
DO $$
DECLARE
  orphan_binding RECORD;
BEGIN
  FOR orphan_binding IN
    SELECT
      channel_row.id AS channel_id,
      channel_row.group_manager_agent_id AS agent_id
    FROM channel channel_row
    WHERE channel_row.group_manager_agent_id IS NOT NULL
      AND NOT EXISTS (
        SELECT 1
        FROM channel_member membership
          WHERE membership.workspace_id = channel_row.workspace_id
          AND membership.channel_id = channel_row.id
          AND membership.member_type = 'agent'
          AND membership.member_id = channel_row.group_manager_agent_id
      )
  LOOP
    RAISE NOTICE
      'discarding orphan legacy group manager binding channel_id=% agent_id=%',
      orphan_binding.channel_id,
      orphan_binding.agent_id;
  END LOOP;
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

-- Remove the retired singleton column without leaving the protected general
-- channel functions referring to a field that no longer exists.
CREATE OR REPLACE FUNCTION ensure_system_general_channel(
  target_workspace_id UUID,
  creator_user_id UUID
)
RETURNS UUID
LANGUAGE plpgsql
AS $$
DECLARE
  general_channel_id UUID;
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM member
    WHERE workspace_id = target_workspace_id
      AND user_id = creator_user_id
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_creator_must_be_workspace_member';
  END IF;

  INSERT INTO channel (
    workspace_id,
    name,
    description,
    created_by,
    kind,
    system_key
  )
  VALUES (
    target_workspace_id,
    'general',
    'Workspace-wide conversation',
    creator_user_id,
    'group',
    'general'
  )
  ON CONFLICT (workspace_id, system_key) WHERE system_key IS NOT NULL
  DO NOTHING
  RETURNING id INTO general_channel_id;

  IF general_channel_id IS NULL THEN
    SELECT id
    INTO general_channel_id
    FROM channel
    WHERE workspace_id = target_workspace_id
      AND system_key = 'general'
    FOR UPDATE;
  END IF;

  IF general_channel_id IS NULL OR EXISTS (
    SELECT 1
    FROM channel
    WHERE id = general_channel_id
      AND (
        workspace_id IS DISTINCT FROM target_workspace_id
        OR name IS DISTINCT FROM 'general'
        OR kind IS DISTINCT FROM 'group'
        OR system_key IS DISTINCT FROM 'general'
        OR archived_at IS NOT NULL
        OR project_id IS NOT NULL
        OR lark_chat_id IS NOT NULL
      )
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_identity_invalid';
  END IF;

  INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
  SELECT general_channel_id, target_workspace_id, 'user', workspace_member.user_id
  FROM member workspace_member
  WHERE workspace_member.workspace_id = target_workspace_id
  UNION ALL
  SELECT general_channel_id, target_workspace_id, 'agent', workspace_agent.id
  FROM agent workspace_agent
  WHERE workspace_agent.workspace_id = target_workspace_id
    AND workspace_agent.visibility = 'workspace'
    AND workspace_agent.archived_at IS NULL
  ON CONFLICT DO NOTHING;

  DELETE FROM channel_member channel_roster
  WHERE channel_roster.channel_id = general_channel_id
    AND channel_roster.workspace_id = target_workspace_id
    AND (
      (channel_roster.member_type = 'user' AND NOT EXISTS (
        SELECT 1
        FROM member workspace_member
        WHERE workspace_member.workspace_id = target_workspace_id
          AND workspace_member.user_id = channel_roster.member_id
      ))
      OR
      (channel_roster.member_type = 'agent' AND NOT EXISTS (
        SELECT 1
        FROM agent workspace_agent
        WHERE workspace_agent.workspace_id = target_workspace_id
          AND workspace_agent.id = channel_roster.member_id
          AND workspace_agent.visibility = 'workspace'
          AND workspace_agent.archived_at IS NULL
      ))
    );

  RETURN general_channel_id;
END;
$$;

CREATE OR REPLACE FUNCTION guard_system_general_channel()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.system_key = 'general' THEN
      IF NEW.name IS DISTINCT FROM 'general'
         OR NEW.kind IS DISTINCT FROM 'group'
         OR NEW.archived_at IS NOT NULL
         OR NEW.project_id IS NOT NULL
         OR NEW.lark_chat_id IS NOT NULL THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_identity_invalid';
      END IF;
    ELSIF NEW.name = 'general' THEN
      RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_name_reserved';
    END IF;
    RETURN NEW;
  END IF;

  IF TG_OP = 'DELETE' THEN
    IF OLD.system_key = 'general' AND EXISTS (
      SELECT 1 FROM workspace WHERE id = OLD.workspace_id
    ) THEN
      RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_channel_protected';
    END IF;
    RETURN OLD;
  END IF;

  IF OLD.system_key = 'general' THEN
    IF NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.name IS DISTINCT FROM 'general'
       OR NEW.kind IS DISTINCT FROM 'group'
       OR NEW.system_key IS DISTINCT FROM 'general'
       OR NEW.archived_at IS NOT NULL
       OR NEW.archived_by IS NOT NULL
       OR NEW.project_id IS NOT NULL
       OR NEW.lark_chat_id IS NOT NULL
       OR NEW.description IS DISTINCT FROM OLD.description
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
      RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_channel_protected';
    END IF;
  ELSIF NEW.system_key = 'general' OR NEW.name = 'general' THEN
    RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_name_reserved';
  END IF;

  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS cancel_group_manager_reminders_for_channel_trigger ON channel;
DROP FUNCTION IF EXISTS cancel_group_manager_reminders_for_channel();

DROP INDEX IF EXISTS idx_channel_group_manager_agent;
ALTER TABLE channel
  DROP CONSTRAINT IF EXISTS channel_group_manager_agent_id_fkey;
ALTER TABLE channel DROP COLUMN group_manager_agent_id;

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

DROP TRIGGER IF EXISTS cancel_group_manager_reminders_for_membership_trigger ON channel_member;
DROP FUNCTION IF EXISTS cancel_group_manager_reminders_for_membership();

-- The disabled Wendy/Beckham ambient scheduler and its durable watch store are
-- removed rather than kept as a compatibility no-op.
DROP TABLE wendy_channel_ambient;

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
