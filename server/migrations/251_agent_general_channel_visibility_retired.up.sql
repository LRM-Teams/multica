BEGIN;

-- Task #908 (agent visibility mechanism retirement): every non-archived
-- agent in a workspace now belongs in that workspace's system "general"
-- channel — membership eligibility no longer depends on agent.visibility,
-- only on the agent existing and not being archived. Frank, #multica thread
-- f83df812, 2026-07-30 10:55-11:01: "所有的代码全部删掉，默认public的";
-- Parker, same thread, 2026-07-30 11:00: DB triggers keyed on
-- visibility='workspace' become unconditional as part of this cut
-- ("同事就该在 general 里找得到").
--
-- This changes FUTURE trigger firings and future ensure_system_general_channel
-- calls only — no backfill of existing workspaces (Frank, same thread,
-- 2026-07-30 10:38: "历史数据不用管，后面按这套方案走就行"). A previously
-- excluded private agent joins general the next time something updates that
-- agent row, or the next time ensure_system_general_channel runs for its
-- workspace — not retroactively via this migration.

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
    old_was_eligible := OLD.archived_at IS NULL;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    new_is_eligible := NEW.archived_at IS NULL;
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

-- No longer needs to re-fire on visibility changes — eligibility doesn't
-- read that column anymore. Dropping it from the watched-columns list here
-- (rather than leaving it and cleaning up later) means the later migration
-- that drops agent.visibility entirely has nothing referencing it left.
DROP TRIGGER IF EXISTS trg_sync_system_general_agent ON agent;
CREATE TRIGGER trg_sync_system_general_agent
AFTER INSERT OR UPDATE OF workspace_id, archived_at OR DELETE ON agent
FOR EACH ROW
EXECUTE FUNCTION sync_system_general_agent();

-- guard_system_general_roster is a separate BEFORE trigger on channel_member
-- that independently re-validates every INSERT/DELETE against the general
-- roster using the same eligibility rule — it must drop the
-- visibility='workspace' condition too, or it will reject the now-eligible
-- rows that sync_system_general_agent above tries to insert/delete.
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
          AND workspace_agent.archived_at IS NULL
      ))
    );

  RETURN general_channel_id;
END;
$$;

COMMIT;
