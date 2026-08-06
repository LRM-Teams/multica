-- Barry #1286 workspace_id escape + upgrade audit for 239 window damage.
-- 1) Install eligible-owner helper (same workspace + still member)
-- 2) Re-check on member DELETE
-- 3) Full-table audit/repair of surviving ordinary groups (must not leave 0 eligible)

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
    RETURN;
  END IF;

  IF NOT ordinary THEN
    RETURN;
  END IF;

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

-- Full-table repair of ordinary groups damaged during 239 window.
DO $$
DECLARE
  r record;
  eligible int;
  fixed_ws int;
  promoted int;
  inserted int;
  still_bad int := 0;
  bad_ids text := '';
BEGIN
  FOR r IN
    SELECT c.id, c.workspace_id, c.created_by
    FROM channel c
    WHERE c.kind = 'group' AND c.system_key IS NULL
  LOOP
    SELECT count(*) INTO eligible
    FROM channel_member cm
    JOIN member m
      ON m.workspace_id = r.workspace_id
     AND m.user_id = cm.member_id
    WHERE cm.channel_id = r.id
      AND cm.workspace_id = r.workspace_id
      AND cm.role = 'owner'
      AND cm.member_type = 'user';

    IF eligible > 0 THEN
      CONTINUE;
    END IF;

    -- 1) Align owner-row workspace_id to channel when user is member of channel.ws
    UPDATE channel_member cm
    SET workspace_id = r.workspace_id
    WHERE cm.channel_id = r.id
      AND cm.role = 'owner'
      AND cm.member_type = 'user'
      AND cm.workspace_id IS DISTINCT FROM r.workspace_id
      AND EXISTS (
        SELECT 1 FROM member m
        WHERE m.workspace_id = r.workspace_id AND m.user_id = cm.member_id
      );
    GET DIAGNOSTICS fixed_ws = ROW_COUNT;

    SELECT count(*) INTO eligible
    FROM channel_member cm
    JOIN member m
      ON m.workspace_id = r.workspace_id
     AND m.user_id = cm.member_id
    WHERE cm.channel_id = r.id
      AND cm.workspace_id = r.workspace_id
      AND cm.role = 'owner'
      AND cm.member_type = 'user';
    IF eligible > 0 THEN
      CONTINUE;
    END IF;

    -- 2) Demote any non-eligible owners, promote earliest eligible human member
    UPDATE channel_member cm
    SET role = 'member'
    WHERE cm.channel_id = r.id
      AND cm.role = 'owner'
      AND cm.member_type = 'user'
      AND NOT EXISTS (
        SELECT 1 FROM member m
        WHERE m.workspace_id = r.workspace_id
          AND m.user_id = cm.member_id
          AND cm.workspace_id = r.workspace_id
      );

    UPDATE channel_member cm
    SET role = 'owner'
    FROM (
      SELECT cm2.member_type, cm2.member_id
      FROM channel_member cm2
      JOIN member m
        ON m.workspace_id = r.workspace_id
       AND m.user_id = cm2.member_id
      WHERE cm2.channel_id = r.id
        AND cm2.workspace_id = r.workspace_id
        AND cm2.member_type = 'user'
      ORDER BY cm2.created_at ASC, cm2.member_id ASC
      LIMIT 1
    ) pick
    WHERE cm.channel_id = r.id
      AND cm.member_type = pick.member_type
      AND cm.member_id = pick.member_id;
    GET DIAGNOSTICS promoted = ROW_COUNT;

    SELECT count(*) INTO eligible
    FROM channel_member cm
    JOIN member m
      ON m.workspace_id = r.workspace_id
     AND m.user_id = cm.member_id
    WHERE cm.channel_id = r.id
      AND cm.workspace_id = r.workspace_id
      AND cm.role = 'owner'
      AND cm.member_type = 'user';
    IF eligible > 0 THEN
      CONTINUE;
    END IF;

    -- 3) Insert created_by as owner when still a workspace member
    IF r.created_by IS NOT NULL AND EXISTS (
      SELECT 1 FROM member m
      WHERE m.workspace_id = r.workspace_id AND m.user_id = r.created_by
    ) THEN
      INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
      VALUES (r.id, r.workspace_id, 'user', r.created_by, 'owner')
      ON CONFLICT (channel_id, member_type, member_id) DO UPDATE
        SET role = 'owner', workspace_id = EXCLUDED.workspace_id;
      GET DIAGNOSTICS inserted = ROW_COUNT;
    END IF;

    SELECT count(*) INTO eligible
    FROM channel_member cm
    JOIN member m
      ON m.workspace_id = r.workspace_id
     AND m.user_id = cm.member_id
    WHERE cm.channel_id = r.id
      AND cm.workspace_id = r.workspace_id
      AND cm.role = 'owner'
      AND cm.member_type = 'user';
    IF eligible = 0 THEN
      still_bad := still_bad + 1;
      bad_ids := bad_ids || r.id::text || ',';
    END IF;
  END LOOP;

  IF still_bad > 0 THEN
    RAISE EXCEPTION
      'migration 240: % ordinary group(s) still lack an eligible human owner after repair; channels: %',
      still_bad, bad_ids;
  END IF;
END $$;
