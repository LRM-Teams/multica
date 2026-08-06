-- Restores the schema-only shape from before 252 (home_channel_id column,
-- both CHECK constraints allowing 'channel'). Does not restore historical
-- visibility='channel' data converted to 'private' by the up migration —
-- that conversion is not reversible from the row alone.

ALTER TABLE agent
  ADD COLUMN IF NOT EXISTS home_channel_id UUID REFERENCES channel(id) ON DELETE RESTRICT;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_visibility_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_visibility_check
  CHECK (visibility IN ('workspace', 'private', 'channel'));

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

ALTER TABLE agent_creation_draft DROP CONSTRAINT IF EXISTS agent_creation_draft_visibility_check;
ALTER TABLE agent_creation_draft
  ADD CONSTRAINT agent_creation_draft_visibility_check
  CHECK (visibility IN ('workspace', 'private', 'channel'));
