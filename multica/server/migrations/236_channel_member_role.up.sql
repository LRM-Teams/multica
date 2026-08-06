-- Channel-level roles (Beckham v2 §4 / Frank+Parker lock):
--   owner   = 群主 (exactly one human per channel; default channel creator)
--   manager = 群管(agent) / 管理员(human); 0..N
--   member  = default
-- Distinct from workspace MemberRole (owner/admin/member). Never conflate.
--
-- Slice 1: data model + backfill + read surface only.
-- Permission mutations (set role / transfer owner / kick) wait for #801.

ALTER TABLE channel_member
  ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member';

ALTER TABLE channel_member
  DROP CONSTRAINT IF EXISTS channel_member_role_check;
ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_role_check
  CHECK (role IN ('owner', 'manager', 'member'));

-- Exactly one owner per channel (product: 恰 1 群主).
CREATE UNIQUE INDEX IF NOT EXISTS channel_member_one_owner
  ON channel_member (channel_id)
  WHERE role = 'owner';

-- Helpful for member-panel sort: owner → manager(agent) → manager(user) → member.
CREATE INDEX IF NOT EXISTS channel_member_channel_role
  ON channel_member (channel_id, role, member_type);

-- Backfill owner = channel.created_by when that user is still a member.
UPDATE channel_member cm
SET role = 'owner'
FROM channel c
WHERE cm.channel_id = c.id
  AND cm.member_type = 'user'
  AND cm.member_id = c.created_by
  AND cm.role = 'member';

-- Channels whose creator is no longer a member: promote earliest remaining
-- human member to owner (deterministic). Agents are never auto-promoted.
WITH candidates AS (
  SELECT DISTINCT ON (cm.channel_id)
    cm.channel_id,
    cm.member_type,
    cm.member_id
  FROM channel_member cm
  WHERE cm.member_type = 'user'
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
