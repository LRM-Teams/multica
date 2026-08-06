-- LRM-370 / LRM-240: agent visibility=channel ("仅本群可见") binds a single
-- home_channel_id. Illegal combinations are rejected by CHECK — no silent
-- fallback to workspace/private (LRM-238).

ALTER TABLE agent
  ADD COLUMN IF NOT EXISTS home_channel_id UUID REFERENCES channel(id) ON DELETE RESTRICT;

-- Expand visibility enum to include channel.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_visibility_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_visibility_check
  CHECK (visibility IN ('workspace', 'private', 'channel'));

-- channel visibility requires a home channel; other visibilities forbid one.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_home_channel_visibility_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_home_channel_visibility_check
  CHECK (
    (visibility = 'channel' AND home_channel_id IS NOT NULL)
    OR (visibility <> 'channel' AND home_channel_id IS NULL)
  );

CREATE INDEX IF NOT EXISTS agent_home_channel_id_idx
  ON agent (workspace_id, home_channel_id)
  WHERE home_channel_id IS NOT NULL;

-- Agent creation drafts: allow channel visibility (home channel is chosen at
-- finalize time via the agent create/update API, not on the draft row).
ALTER TABLE agent_creation_draft DROP CONSTRAINT IF EXISTS agent_creation_draft_visibility_check;
ALTER TABLE agent_creation_draft
  ADD CONSTRAINT agent_creation_draft_visibility_check
  CHECK (visibility IN ('workspace', 'private', 'channel'));

-- LRM-240 / LRM-370: migrate group managers (贝克汉姆) from private → channel
-- bound to their home group. Existing memberships in other channels are left
-- alone (no auto-kick); discovery/invite/@ filtering uses home_channel_id.
UPDATE agent a
SET visibility = 'channel',
    home_channel_id = c.id,
    updated_at = now()
FROM channel c
WHERE c.group_manager_agent_id = a.id
  AND a.managed_role = 'group_manager'
  AND a.archived_at IS NULL
  AND a.visibility = 'private'
  AND a.home_channel_id IS NULL;
