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

-- 5. Repair empty ordinary groups: insert created_by as human owner when the
--    channel has no human members at all (orphan rows after 236).
INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
SELECT c.id, c.workspace_id, 'user', c.created_by, 'owner'
FROM channel c
WHERE c.kind = 'group'
  AND c.system_key IS NULL
  AND c.created_by IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM channel_member cm
    WHERE cm.channel_id = c.id AND cm.member_type = 'user'
  )
ON CONFLICT DO NOTHING;

-- Re-run owner backfill for those freshly inserted members.
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

-- 7. Write boundary: ordinary groups cannot lose their last human owner
--    via DELETE or role demotion (Parker: invariant on every path).
CREATE OR REPLACE FUNCTION channel_member_preserve_human_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  ordinary boolean;
  remaining int;
BEGIN
  SELECT (c.kind = 'group' AND c.system_key IS NULL)
  INTO ordinary
  FROM channel c
  WHERE c.id = COALESCE(OLD.channel_id, NEW.channel_id);

  IF NOT ordinary THEN
    IF TG_OP = 'DELETE' THEN
      RETURN OLD;
    END IF;
    RETURN NEW;
  END IF;

  -- Only care when we remove or demote a human owner.
  IF TG_OP = 'DELETE' THEN
    IF OLD.role = 'owner' AND OLD.member_type = 'user' THEN
      -- BEFORE DELETE: OLD is still visible; exclude self explicitly.
      SELECT count(*) INTO remaining
      FROM channel_member
      WHERE channel_id = OLD.channel_id
        AND role = 'owner'
        AND member_type = 'user'
        AND member_id IS DISTINCT FROM OLD.member_id;
      IF remaining = 0 THEN
        RAISE EXCEPTION 'cannot remove the only human owner of an ordinary group channel'
          USING ERRCODE = 'check_violation';
      END IF;
    END IF;
    RETURN OLD;
  END IF;

  IF TG_OP = 'UPDATE' THEN
    IF OLD.role = 'owner' AND OLD.member_type = 'user'
       AND (NEW.role IS DISTINCT FROM 'owner' OR NEW.member_type IS DISTINCT FROM 'user') THEN
      SELECT count(*) INTO remaining
      FROM channel_member
      WHERE channel_id = OLD.channel_id
        AND role = 'owner'
        AND member_type = 'user'
        AND member_id IS DISTINCT FROM OLD.member_id;
      IF remaining = 0 THEN
        RAISE EXCEPTION 'cannot demote the only human owner of an ordinary group channel'
          USING ERRCODE = 'check_violation';
      END IF;
    END IF;
    RETURN NEW;
  END IF;

  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_member_preserve_human_owner ON channel_member;
CREATE TRIGGER trg_channel_member_preserve_human_owner
  BEFORE DELETE OR UPDATE OF role, member_type ON channel_member
  FOR EACH ROW
  EXECUTE FUNCTION channel_member_preserve_human_owner();
