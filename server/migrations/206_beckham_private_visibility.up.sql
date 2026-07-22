-- LRM-233: Beckham (group_manager) is private for invite/discover.
--
-- New Beckhams are created with visibility='private' in application code.
-- This migration flips existing ones and keeps #general roster sync from
-- auto-kicking them (issue default: do not remove existing memberships).

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
        OR group_manager_agent_id IS NOT NULL
      )
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_identity_invalid';
  END IF;

  -- Only workspace-visible agents are auto-added. Private Beckhams are not
  -- discoverable / inviteable and therefore never join #general by default.
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

  -- Preserve existing live group_manager members even when private (no auto-kick).
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
          AND workspace_agent.archived_at IS NULL
          AND (
            workspace_agent.visibility = 'workspace'
            OR workspace_agent.managed_role = 'group_manager'
          )
      ))
    );

  RETURN general_channel_id;
END;
$$;

CREATE OR REPLACE FUNCTION sync_system_general_agent()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  general_channel_id UUID;
  old_was_eligible BOOLEAN := FALSE;
  new_is_eligible BOOLEAN := FALSE;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    old_was_eligible := OLD.visibility = 'workspace' AND OLD.archived_at IS NULL;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    new_is_eligible := NEW.visibility = 'workspace' AND NEW.archived_at IS NULL;
  END IF;

  IF old_was_eligible AND (
    TG_OP = 'DELETE'
    OR NOT new_is_eligible
    OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id
    OR OLD.id IS DISTINCT FROM NEW.id
  ) THEN
    -- Do not auto-remove a live group manager when it becomes private.
    IF TG_OP = 'DELETE'
       OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id
       OR OLD.id IS DISTINCT FROM NEW.id
       OR NEW.archived_at IS NOT NULL
       OR COALESCE(NEW.managed_role, '') IS DISTINCT FROM 'group_manager'
    THEN
      SELECT id INTO general_channel_id
      FROM channel
      WHERE workspace_id = OLD.workspace_id AND system_key = 'general';
      IF general_channel_id IS NOT NULL THEN
        DELETE FROM channel_member
        WHERE channel_id = general_channel_id
          AND workspace_id = OLD.workspace_id
          AND member_type = 'agent'
          AND member_id = OLD.id;
      END IF;
    END IF;
  END IF;

  IF new_is_eligible THEN
    SELECT id INTO general_channel_id
    FROM channel
    WHERE workspace_id = NEW.workspace_id AND system_key = 'general';
    IF general_channel_id IS NOT NULL THEN
      INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
      VALUES (general_channel_id, NEW.workspace_id, 'agent', NEW.id)
      ON CONFLICT DO NOTHING;
    END IF;
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

UPDATE agent
SET visibility = 'private', updated_at = now()
WHERE managed_role = 'group_manager'
  AND visibility IS DISTINCT FROM 'private'
  AND archived_at IS NULL;
