-- Upgrade path for envs that already applied 237 v1/v2. Idempotent.
-- Closes Barry workspace_id escape: eligible owner = same workspace + still member.


-- Auto-seed human owner on ordinary group INSERT when created_by is a current
-- workspace member. Closes the "bare INSERT leaves 0 owners" hole without
-- accepting ghost owners. Final deferred check still fails when created_by is
-- missing / not a workspace member / agent-only roster.
CREATE OR REPLACE FUNCTION channel_seed_human_owner_on_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.kind = 'group' AND NEW.system_key IS NULL AND NEW.created_by IS NOT NULL THEN
    IF EXISTS (
      SELECT 1 FROM member m
      WHERE m.workspace_id = NEW.workspace_id
        AND m.user_id = NEW.created_by
    ) THEN
      INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
      VALUES (NEW.id, NEW.workspace_id, 'user', NEW.created_by, 'owner')
      ON CONFLICT (channel_id, member_type, member_id) DO UPDATE
        SET role = CASE
          WHEN channel_member.role = 'owner' THEN channel_member.role
          ELSE 'owner'
        END;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_seed_human_owner_on_insert ON channel;
CREATE TRIGGER trg_channel_seed_human_owner_on_insert
  AFTER INSERT ON channel
  FOR EACH ROW
  EXECUTE FUNCTION channel_seed_human_owner_on_insert();

CREATE OR REPLACE FUNCTION assert_ordinary_group_has_human_owner(ch uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  ordinary boolean;
  n int;
BEGIN
  IF ch IS NULL THEN
    RETURN;
  END IF;

  SELECT (c.kind = 'group' AND c.system_key IS NULL)
  INTO ordinary
  FROM channel c
  WHERE c.id = ch;

  IF NOT FOUND THEN
    -- Channel already deleted (cascade cleanup): no surviving ordinary group.
    RETURN;
  END IF;

  IF NOT ordinary THEN
    RETURN;
  END IF;

  -- Eligible human owner: same workspace as channel AND still a workspace member.
  -- Bare channel_id count was Barry's workspace_id escape (cross-ws / ghost).
  SELECT count(*) INTO n
  FROM channel c
  JOIN channel_member cm
    ON cm.channel_id = c.id
   AND cm.workspace_id = c.workspace_id
   AND cm.role = 'owner'
   AND cm.member_type = 'user'
  JOIN member m
    ON m.workspace_id = c.workspace_id
   AND m.user_id = cm.member_id
  WHERE c.id = ch;

  IF n = 0 THEN
    RAISE EXCEPTION 'ordinary group must have at least one human owner'
      USING ERRCODE = 'check_violation';
  END IF;
END;
$$;


CREATE OR REPLACE FUNCTION channel_member_assert_human_owner_final()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    PERFORM assert_ordinary_group_has_human_owner(OLD.channel_id);
  ELSIF TG_OP = 'UPDATE' THEN
    PERFORM assert_ordinary_group_has_human_owner(NEW.channel_id);
    IF OLD.channel_id IS DISTINCT FROM NEW.channel_id THEN
      PERFORM assert_ordinary_group_has_human_owner(OLD.channel_id);
    END IF;
  ELSE
    PERFORM assert_ordinary_group_has_human_owner(NEW.channel_id);
  END IF;
  RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION channel_assert_human_owner_final()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM assert_ordinary_group_has_human_owner(NEW.id);
  RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_member_assert_human_owner ON channel_member;
CREATE CONSTRAINT TRIGGER trg_channel_member_assert_human_owner
  AFTER INSERT OR UPDATE OF role, member_type, channel_id, workspace_id OR DELETE ON channel_member
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW
  EXECUTE FUNCTION channel_member_assert_human_owner_final();

DROP TRIGGER IF EXISTS trg_channel_assert_human_owner ON channel;
CREATE CONSTRAINT TRIGGER trg_channel_assert_human_owner
  AFTER INSERT OR UPDATE OF kind, system_key, workspace_id ON channel
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW
  EXECUTE FUNCTION channel_assert_human_owner_final();

-- When a workspace membership is removed/moved, re-check ordinary groups where
-- that user is (was) a channel owner so ghost sole owners cannot survive.
CREATE OR REPLACE FUNCTION workspace_member_assert_channel_owner_final()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  r record;
  uid uuid;
  ws uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN
    uid := OLD.user_id;
    ws := OLD.workspace_id;
  ELSE
    -- UPDATE of user_id / workspace_id: check OLD binding that may have lost eligibility.
    uid := OLD.user_id;
    ws := OLD.workspace_id;
  END IF;

  FOR r IN
    SELECT c.id AS channel_id
    FROM channel c
    JOIN channel_member cm
      ON cm.channel_id = c.id
     AND cm.member_type = 'user'
     AND cm.member_id = uid
     AND cm.role = 'owner'
    WHERE c.workspace_id = ws
      AND c.kind = 'group'
      AND c.system_key IS NULL
  LOOP
    PERFORM assert_ordinary_group_has_human_owner(r.channel_id);
  END LOOP;

  IF TG_OP = 'UPDATE' AND (
       OLD.user_id IS DISTINCT FROM NEW.user_id
    OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id
  ) THEN
    FOR r IN
      SELECT c.id AS channel_id
      FROM channel c
      JOIN channel_member cm
        ON cm.channel_id = c.id
       AND cm.member_type = 'user'
       AND cm.member_id = NEW.user_id
       AND cm.role = 'owner'
      WHERE c.workspace_id = NEW.workspace_id
        AND c.kind = 'group'
        AND c.system_key IS NULL
    LOOP
      PERFORM assert_ordinary_group_has_human_owner(r.channel_id);
    END LOOP;
  END IF;

  RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_workspace_member_assert_channel_owner ON member;
CREATE CONSTRAINT TRIGGER trg_workspace_member_assert_channel_owner
  AFTER DELETE OR UPDATE OF user_id, workspace_id ON member
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW
  EXECUTE FUNCTION workspace_member_assert_channel_owner_final();
