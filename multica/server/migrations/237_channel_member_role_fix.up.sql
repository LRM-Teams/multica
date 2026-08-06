-- Successor to 236 (already applied on dev). Do not rewrite 236.
-- Barry SOURCE BLOCK on #1284:
--   1) role semantics only for ordinary non-system groups
--   2) owner must be human; zero/agent owner not product-legal for groups
--   3) DM / system_key channels must not carry elevated roles

-- 1. Demote any agent owners (agents cannot be channel owner).
UPDATE channel_member
SET role = 'member'
WHERE role = 'owner' AND member_type = 'agent';

-- 2. Non-group and system channels: force all roles back to member.
--    Channel roles are product-only for ordinary groups (kind='group' AND system_key IS NULL).
UPDATE channel_member cm
SET role = 'member'
FROM channel c
WHERE cm.channel_id = c.id
  AND (c.kind IS DISTINCT FROM 'group' OR c.system_key IS NOT NULL)
  AND cm.role <> 'member';

-- 3. Ordinary groups: clear multi/wrong owners then re-backfill human owner.
--    First demote every owner on ordinary groups so we can re-pick deterministically.
UPDATE channel_member cm
SET role = 'member'
FROM channel c
WHERE cm.channel_id = c.id
  AND c.kind = 'group'
  AND c.system_key IS NULL
  AND cm.role = 'owner';

-- Prefer channel.created_by when still a user member.
UPDATE channel_member cm
SET role = 'owner'
FROM channel c
WHERE cm.channel_id = c.id
  AND c.kind = 'group'
  AND c.system_key IS NULL
  AND cm.member_type = 'user'
  AND cm.member_id = c.created_by;

-- Else earliest remaining human member (deterministic).
WITH candidates AS (
  SELECT DISTINCT ON (cm.channel_id)
    cm.channel_id,
    cm.member_type,
    cm.member_id
  FROM channel_member cm
  JOIN channel c ON c.id = cm.channel_id
  WHERE c.kind = 'group'
    AND c.system_key IS NULL
    AND cm.member_type = 'user'
    AND NOT EXISTS (
      SELECT 1 FROM channel_member o
      WHERE o.channel_id = cm.channel_id AND o.role = 'owner'
    )
  ORDER BY cm.channel_id, cm.created_at ASC, cm.member_id ASC
)
UPDATE channel_member cm
SET role = 'owner'
FROM candidates c
WHERE cm.channel_id = c.channel_id
  AND cm.member_type = c.member_type
  AND cm.member_id = c.member_id;

-- 4. CHECK: owner ⇒ human. Replace 236 role check.
ALTER TABLE channel_member
  DROP CONSTRAINT IF EXISTS channel_member_role_check;
ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_role_check
  CHECK (
    role IN ('owner', 'manager', 'member')
    AND (role <> 'owner' OR member_type = 'user')
  );

-- 5. Repair empty ordinary groups: insert created_by as human owner only when
--    they are still a workspace member (Barry: no ghost owners).
INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
SELECT c.id, c.workspace_id, 'user', c.created_by, 'owner'
FROM channel c
JOIN member m
  ON m.workspace_id = c.workspace_id
 AND m.user_id = c.created_by
WHERE c.kind = 'group'
  AND c.system_key IS NULL
  AND c.created_by IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM channel_member cm
    WHERE cm.channel_id = c.id AND cm.member_type = 'user'
  )
ON CONFLICT DO NOTHING;

-- Re-run owner backfill for those freshly inserted members / existing members.
UPDATE channel_member cm
SET role = 'owner'
FROM channel c
WHERE cm.channel_id = c.id
  AND c.kind = 'group'
  AND c.system_key IS NULL
  AND cm.member_type = 'user'
  AND cm.member_id = c.created_by
  AND NOT EXISTS (
    SELECT 1 FROM channel_member o
    WHERE o.channel_id = cm.channel_id AND o.role = 'owner' AND o.member_type = 'user'
  );

-- 6. Fail-closed: ordinary groups must have ≥1 human owner after all repairs.
DO $$
DECLARE
  bad_count int;
BEGIN
  SELECT count(*) INTO bad_count
  FROM channel c
  WHERE c.kind = 'group'
    AND c.system_key IS NULL
    AND NOT EXISTS (
      SELECT 1 FROM channel_member cm
      WHERE cm.channel_id = c.id
        AND cm.role = 'owner'
        AND cm.member_type = 'user'
    );
  IF bad_count > 0 THEN
    RAISE EXCEPTION
      'migration 237: % ordinary group(s) still lack a human owner after backfill; fix roster before re-running',
      bad_count;
  END IF;
END $$;

-- 7. Final-state invariant for ordinary groups (Barry P0 / re-BLOCK):
--    - max 1 human owner: partial unique index from 236
--    - min 1 human owner: DEFERRABLE constraint triggers so
--      * DELETE channel / workspace cascade is legal (no surviving group)
--      * atomic transfer (demote old + promote new) can commit
--      * sole-owner leave / demote still fails at commit
--      * bare channel INSERT (0 members) fails at commit for ordinary groups
--      * dm/system → ordinary conversion with 0 owners fails at commit
--      * moving sole owner via channel_id update fails at commit
--
-- Two fact sources must both be covered:
--   A) channel INSERT / kind|system_key|workspace_id change
--   B) channel_member INSERT/DELETE and channel_id|role|member_type|workspace_id change
--      (UPDATE checks OLD∪NEW channel, de-duplicated)

-- Drop the eager BEFORE trigger if a prior tip of this PR created it.
DROP TRIGGER IF EXISTS trg_channel_member_preserve_human_owner ON channel_member;
DROP FUNCTION IF EXISTS channel_member_preserve_human_owner();

-- Shared helper: assert one surviving ordinary group has ≥1 human owner.
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
    -- Moving a member between channels must re-check the source channel too.
    IF OLD.channel_id IS DISTINCT FROM NEW.channel_id THEN
      PERFORM assert_ordinary_group_has_human_owner(OLD.channel_id);
    END IF;
  ELSE
    -- INSERT
    PERFORM assert_ordinary_group_has_human_owner(NEW.channel_id);
  END IF;
  RETURN NULL;
END;
$$;


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

CREATE OR REPLACE FUNCTION channel_assert_human_owner_final()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  -- INSERT / UPDATE of kind|system_key|workspace_id: final state of NEW must
  -- hold a human owner when NEW is an ordinary group. DELETE is not needed —
  -- a deleted channel is not a surviving ordinary group.
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
