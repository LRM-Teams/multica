-- Barry #1286 workspace_id escape: eligible owner must share channel workspace
-- and still be a workspace member. Also re-check on member removal.

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
