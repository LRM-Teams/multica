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

-- 5. Unique one-owner remains (from 236). Document: max-one is DB-enforced;
--    at-least-one for ordinary groups is enforced by CreateChannel transaction
--    + this backfill; empty-member groups (edge) stay without owner until a
--    human joins (future mutation path after #801).
