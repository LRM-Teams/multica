DROP TRIGGER IF EXISTS trg_workspace_member_assert_channel_owner ON member;
DROP FUNCTION IF EXISTS workspace_member_assert_channel_owner_final();
DROP TRIGGER IF EXISTS trg_channel_seed_human_owner_on_insert ON channel;
DROP FUNCTION IF EXISTS channel_seed_human_owner_on_insert();
DROP TRIGGER IF EXISTS trg_channel_assert_human_owner ON channel;
DROP FUNCTION IF EXISTS channel_assert_human_owner_final();

-- Restore 237 v1 member-only trigger surface (UPDATE OF role, member_type only).
CREATE OR REPLACE FUNCTION channel_member_assert_human_owner_final()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  ch uuid;
  ordinary boolean;
  n int;
BEGIN
  IF TG_OP = 'DELETE' THEN
    ch := OLD.channel_id;
  ELSE
    ch := NEW.channel_id;
  END IF;

  SELECT (c.kind = 'group' AND c.system_key IS NULL)
  INTO ordinary
  FROM channel c
  WHERE c.id = ch;

  IF NOT FOUND THEN
    RETURN NULL;
  END IF;

  IF NOT ordinary THEN
    RETURN NULL;
  END IF;

  SELECT count(*) INTO n
  FROM channel_member
  WHERE channel_id = ch
    AND role = 'owner'
    AND member_type = 'user';

  IF n = 0 THEN
    RAISE EXCEPTION 'ordinary group must have at least one human owner'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_member_assert_human_owner ON channel_member;
CREATE CONSTRAINT TRIGGER trg_channel_member_assert_human_owner
  AFTER INSERT OR UPDATE OF role, member_type OR DELETE ON channel_member
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW
  EXECUTE FUNCTION channel_member_assert_human_owner_final();

DROP FUNCTION IF EXISTS assert_ordinary_group_has_human_owner();
