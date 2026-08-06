BEGIN;

LOCK TABLE channel IN ACCESS EXCLUSIVE MODE;
-- Freeze every source/target table that participates in roster convergence
-- until the backfill and lifecycle triggers are installed. Without these
-- locks, a member or agent write that started before CREATE TRIGGER could
-- commit after the backfill and leave #general incomplete.
LOCK TABLE member, agent, channel_member IN SHARE ROW EXCLUSIVE MODE;

-- The collision rename and pristine system-row insert are migration facts, not
-- user-authored channel activity. Suppress the existing radar journal while
-- those writes run; the replacement function below also ignores future system
-- channel writes such as new-workspace creation.
ALTER TABLE channel DISABLE TRIGGER trg_journal_workspace_radar_channel;

ALTER TABLE channel
  ADD COLUMN IF NOT EXISTS system_key TEXT;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'channel_system_key_check'
      AND conrelid = 'channel'::regclass
  ) THEN
    ALTER TABLE channel
      ADD CONSTRAINT channel_system_key_check
      CHECK (system_key IS NULL OR system_key = 'general');
  END IF;
END;
$$;

CREATE UNIQUE INDEX IF NOT EXISTS channel_workspace_system_key_unique
  ON channel (workspace_id, system_key)
  WHERE system_key IS NOT NULL;

CREATE OR REPLACE FUNCTION journal_workspace_radar_channel()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value channel%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  IF row_value.kind <> 'group' OR row_value.system_key = 'general' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  IF TG_OP = 'UPDATE' AND EXISTS (
    SELECT 1
    FROM agent_radar_action action
    JOIN agent_radar_run run ON run.id = action.radar_run_id
    WHERE action.workspace_id = row_value.workspace_id
      AND action.target_id = row_value.id
      AND action.action_type = 'mention_agent'
      AND action.status = 'executing'
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'executing'
  ) THEN
    RETURN NEW;
  END IF;
  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'group_channel', row_value.id, clock_timestamp(),
    'channel', row_value.id,
    jsonb_build_object(
      'channel_id', row_value.id,
      'name', left(row_value.name, 160),
      'description', left(COALESCE(row_value.description, ''), 300),
      'archived_at', row_value.archived_at,
      'updated_at', row_value.updated_at,
      'operation', lower(TG_OP)
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TABLE IF NOT EXISTS system_channel_collision_audit (
  channel_id UUID PRIMARY KEY REFERENCES channel(id) ON DELETE RESTRICT,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  migration_key TEXT NOT NULL DEFAULT '204_system_general_channel',
  original_name TEXT NOT NULL,
  renamed_name TEXT NOT NULL,
  was_archived BOOLEAN NOT NULL,
  message_count BIGINT NOT NULL,
  member_count BIGINT NOT NULL,
  recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, renamed_name),
  CHECK (migration_key = '204_system_general_channel'),
  CHECK (original_name = 'general')
);

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM channel
    WHERE name = 'general'
      AND system_key IS NULL
      AND kind = 'group'
      AND archived_at IS NULL
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_active_name_collision';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM channel
    WHERE name = 'general'
      AND system_key IS NULL
      AND kind <> 'group'
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_non_group_name_collision';
  END IF;
END;
$$;

WITH archived_collisions AS (
  SELECT
    collision.id AS channel_id,
    collision.workspace_id,
    CASE
      WHEN NOT EXISTS (
        SELECT 1
        FROM channel occupied
        WHERE occupied.workspace_id = collision.workspace_id
          AND occupied.id <> collision.id
          AND occupied.name = 'general-archived-' || left(collision.id::text, 8)
      )
      THEN 'general-archived-' || left(collision.id::text, 8)
      ELSE 'general-archived-' || collision.id::text
    END AS renamed_name,
    (SELECT count(*) FROM channel_message message WHERE message.channel_id = collision.id) AS message_count,
    (SELECT count(*) FROM channel_member member WHERE member.channel_id = collision.id) AS member_count
  FROM channel collision
  WHERE collision.name = 'general'
    AND collision.system_key IS NULL
    AND collision.kind = 'group'
    AND collision.archived_at IS NOT NULL
)
INSERT INTO system_channel_collision_audit (
  channel_id,
  workspace_id,
  original_name,
  renamed_name,
  was_archived,
  message_count,
  member_count
)
SELECT
  channel_id,
  workspace_id,
  'general',
  renamed_name,
  TRUE,
  message_count,
  member_count
FROM archived_collisions
ON CONFLICT (channel_id) DO NOTHING;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM system_channel_collision_audit audit
    JOIN channel occupied
      ON occupied.workspace_id = audit.workspace_id
     AND occupied.name = audit.renamed_name
     AND occupied.id <> audit.channel_id
    WHERE audit.migration_key = '204_system_general_channel'
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_archived_rename_collision';
  END IF;
END;
$$;

UPDATE channel collision
SET name = audit.renamed_name,
    updated_at = collision.updated_at
FROM system_channel_collision_audit audit
WHERE collision.id = audit.channel_id
  AND collision.workspace_id = audit.workspace_id
  AND collision.name = audit.original_name
  AND collision.kind = 'group'
  AND collision.archived_at IS NOT NULL
  AND collision.system_key IS NULL;

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

DO $$
DECLARE
  workspace_row RECORD;
BEGIN
  FOR workspace_row IN
    SELECT
      workspace.id AS workspace_id,
      (
        SELECT workspace_member.user_id
        FROM member workspace_member
        WHERE workspace_member.workspace_id = workspace.id
        ORDER BY
          CASE workspace_member.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,
          workspace_member.created_at,
          workspace_member.id
        LIMIT 1
      ) AS creator_user_id
    FROM workspace
    ORDER BY workspace.id
  LOOP
    IF workspace_row.creator_user_id IS NULL THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = 'system_general_workspace_without_member';
    END IF;
    PERFORM ensure_system_general_channel(workspace_row.workspace_id, workspace_row.creator_user_id);
  END LOOP;
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
         OR NEW.lark_chat_id IS NOT NULL
         OR NEW.group_manager_agent_id IS NOT NULL THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_identity_invalid';
      END IF;
    ELSIF NEW.name = 'general' THEN
      RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_name_reserved';
    END IF;
    RETURN NEW;
  END IF;

  IF TG_OP = 'DELETE' THEN
    -- Keep direct channel deletion forbidden, but do not make workspace
    -- deletion impossible: PostgreSQL removes the parent workspace row before
    -- firing the channel's ON DELETE CASCADE action.
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
       OR NEW.group_manager_agent_id IS NOT NULL
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

DROP TRIGGER IF EXISTS trg_guard_system_general_channel ON channel;
CREATE TRIGGER trg_guard_system_general_channel
BEFORE INSERT OR UPDATE OR DELETE ON channel
FOR EACH ROW
EXECUTE FUNCTION guard_system_general_channel();

CREATE OR REPLACE FUNCTION guard_system_general_roster()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_workspace_id UUID;
  old_is_general BOOLEAN := FALSE;
  new_is_general BOOLEAN := FALSE;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    SELECT channel.system_key = 'general', channel.workspace_id
    INTO old_is_general, target_workspace_id
    FROM channel
    WHERE channel.id = OLD.channel_id;
  END IF;

  IF TG_OP <> 'DELETE' THEN
    SELECT channel.system_key = 'general', channel.workspace_id
    INTO new_is_general, target_workspace_id
    FROM channel
    WHERE channel.id = NEW.channel_id;
  END IF;

  IF TG_OP = 'UPDATE' AND (old_is_general OR new_is_general) AND (
    NEW.channel_id IS DISTINCT FROM OLD.channel_id
    OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
    OR NEW.member_type IS DISTINCT FROM OLD.member_type
    OR NEW.member_id IS DISTINCT FROM OLD.member_id
  ) THEN
    RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_roster_managed';
  END IF;

  IF TG_OP = 'INSERT' AND new_is_general THEN
    IF NEW.workspace_id IS DISTINCT FROM target_workspace_id OR NOT (
      (NEW.member_type = 'user' AND EXISTS (
        SELECT 1 FROM member
        WHERE workspace_id = target_workspace_id AND user_id = NEW.member_id
      ))
      OR
      (NEW.member_type = 'agent' AND EXISTS (
        SELECT 1 FROM agent
        WHERE workspace_id = target_workspace_id
          AND id = NEW.member_id
          AND visibility = 'workspace'
          AND archived_at IS NULL
      ))
    ) THEN
      RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_roster_invalid_member';
    END IF;
  END IF;

  IF TG_OP = 'DELETE' AND old_is_general AND (
    (OLD.member_type = 'user' AND EXISTS (
      SELECT 1 FROM member
      WHERE workspace_id = target_workspace_id AND user_id = OLD.member_id
    ))
    OR
    (OLD.member_type = 'agent' AND EXISTS (
      SELECT 1 FROM agent
      WHERE workspace_id = target_workspace_id
        AND id = OLD.member_id
        AND visibility = 'workspace'
        AND archived_at IS NULL
    ))
  ) THEN
    RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_roster_managed';
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_guard_system_general_roster ON channel_member;
CREATE TRIGGER trg_guard_system_general_roster
BEFORE INSERT OR UPDATE OR DELETE ON channel_member
FOR EACH ROW
EXECUTE FUNCTION guard_system_general_roster();

CREATE OR REPLACE FUNCTION sync_system_general_human()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  general_channel_id UUID;
BEGIN
  IF TG_OP IN ('DELETE', 'UPDATE') AND (
    TG_OP = 'DELETE'
    OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id
    OR OLD.user_id IS DISTINCT FROM NEW.user_id
  ) THEN
    SELECT id INTO general_channel_id
    FROM channel
    WHERE workspace_id = OLD.workspace_id AND system_key = 'general';
    IF general_channel_id IS NOT NULL THEN
      DELETE FROM channel_member
      WHERE channel_id = general_channel_id
        AND workspace_id = OLD.workspace_id
        AND member_type = 'user'
        AND member_id = OLD.user_id;
    END IF;
  END IF;

  IF TG_OP IN ('INSERT', 'UPDATE') THEN
    SELECT id INTO general_channel_id
    FROM channel
    WHERE workspace_id = NEW.workspace_id AND system_key = 'general';

    -- Keep the invariant at the database write boundary during a rolling
    -- deploy. A pre-204 server can still create a workspace and its first
    -- member without calling ensure_system_general_channel explicitly after
    -- this migration has committed. The first member write must therefore
    -- create the pristine system channel as well as join the member.
    IF general_channel_id IS NULL THEN
      general_channel_id := ensure_system_general_channel(NEW.workspace_id, NEW.user_id);
    END IF;

    INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
    VALUES (general_channel_id, NEW.workspace_id, 'user', NEW.user_id)
    ON CONFLICT DO NOTHING;
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_sync_system_general_human ON member;
CREATE TRIGGER trg_sync_system_general_human
AFTER INSERT OR UPDATE OF workspace_id, user_id OR DELETE ON member
FOR EACH ROW
EXECUTE FUNCTION sync_system_general_human();

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

DROP TRIGGER IF EXISTS trg_sync_system_general_agent ON agent;
CREATE TRIGGER trg_sync_system_general_agent
AFTER INSERT OR UPDATE OF workspace_id, visibility, archived_at OR DELETE ON agent
FOR EACH ROW
EXECUTE FUNCTION sync_system_general_agent();

ALTER TABLE channel ENABLE TRIGGER trg_journal_workspace_radar_channel;

COMMIT;
