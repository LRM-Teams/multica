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

-- 7. Final-state invariant for ordinary groups (Barry P0):
--    - max 1 human owner: partial unique index from 236
--    - min 1 human owner: DEFERRABLE constraint trigger so
--      * DELETE channel / workspace cascade is legal (no surviving group)
--      * atomic transfer (demote old + promote new) can commit
--      * sole-owner leave / demote still fails at commit
--
-- Drop the eager BEFORE trigger if a prior tip of this PR created it.
DROP TRIGGER IF EXISTS trg_channel_member_preserve_human_owner ON channel_member;
DROP FUNCTION IF EXISTS channel_member_preserve_human_owner();

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
    -- Channel already deleted (cascade cleanup): no surviving ordinary group.
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
  -- max-one is also enforced by channel_member_one_owner unique index
  RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_member_assert_human_owner ON channel_member;
CREATE CONSTRAINT TRIGGER trg_channel_member_assert_human_owner
  AFTER INSERT OR UPDATE OF role, member_type OR DELETE ON channel_member
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW
  EXECUTE FUNCTION channel_member_assert_human_owner_final();
