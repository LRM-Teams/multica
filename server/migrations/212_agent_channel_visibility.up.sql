-- LRM-370 / LRM-240: agent visibility=channel + single home_channel_id.
-- Channel-scoped agents are discoverable/invitable/@-mentionable only in their
-- home group. Existing memberships outside home are kept (no auto-kick).

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_visibility_check;
ALTER TABLE agent ADD CONSTRAINT agent_visibility_check
  CHECK (visibility IN ('workspace', 'private', 'channel'));

ALTER TABLE agent
  ADD COLUMN IF NOT EXISTS home_channel_id UUID REFERENCES channel(id) ON DELETE SET NULL;

-- Pairing rule (LRM-238: no silent fallback):
--   visibility=channel  ↔  home_channel_id IS NOT NULL
--   other visibilities  ↔  home_channel_id IS NULL
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_channel_visibility_home_check;
ALTER TABLE agent ADD CONSTRAINT agent_channel_visibility_home_check
  CHECK (
    (visibility = 'channel' AND home_channel_id IS NOT NULL)
    OR (visibility <> 'channel' AND home_channel_id IS NULL)
  );

CREATE INDEX IF NOT EXISTS agent_home_channel_id_idx
  ON agent (workspace_id, home_channel_id)
  WHERE home_channel_id IS NOT NULL;

-- Drafts may seed channel visibility once FE ships the picker (LRM-371).
ALTER TABLE agent_creation_draft DROP CONSTRAINT IF EXISTS agent_creation_draft_visibility_check;
ALTER TABLE agent_creation_draft ADD CONSTRAINT agent_creation_draft_visibility_check
  CHECK (visibility IN ('workspace', 'private', 'channel'));

-- Migrate group managers (贝克汉姆) from private → channel bound to their group
-- (LRM-240 point 3 / supersedes pure private from LRM-233 / #858).
UPDATE agent AS a
SET visibility = 'channel',
    home_channel_id = c.id,
    updated_at = now()
FROM channel c
WHERE c.group_manager_agent_id = a.id
  AND a.managed_role = 'group_manager'
  AND a.archived_at IS NULL
  AND a.visibility <> 'channel';

-- Managers not bound to any live channel stay private (no invented home).
-- Constraint above requires home NULL when not channel, so leave them private.
