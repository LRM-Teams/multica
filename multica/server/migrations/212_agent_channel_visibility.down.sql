-- Reverse of 212_agent_channel_visibility.

-- Restore group managers to private (LRM-233) before dropping the channel enum.
UPDATE agent a
SET visibility = 'private',
    home_channel_id = NULL,
    updated_at = now()
WHERE a.managed_role = 'group_manager'
  AND a.visibility = 'channel'
  AND a.archived_at IS NULL;

ALTER TABLE agent_creation_draft DROP CONSTRAINT IF EXISTS agent_creation_draft_visibility_check;
ALTER TABLE agent_creation_draft
  ADD CONSTRAINT agent_creation_draft_visibility_check
  CHECK (visibility IN ('workspace', 'private'));

DROP INDEX IF EXISTS agent_home_channel_id_idx;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_home_channel_visibility_check;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_visibility_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_visibility_check
  CHECK (visibility IN ('workspace', 'private'));

ALTER TABLE agent DROP COLUMN IF EXISTS home_channel_id;
