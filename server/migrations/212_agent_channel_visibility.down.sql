-- Reverse LRM-370: drop channel visibility + home_channel_id.

-- Bound group managers fall back to private (LRM-233 posture).
UPDATE agent
SET visibility = 'private',
    home_channel_id = NULL,
    updated_at = now()
WHERE visibility = 'channel';

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_channel_visibility_home_check;

DROP INDEX IF EXISTS agent_home_channel_id_idx;

ALTER TABLE agent DROP COLUMN IF EXISTS home_channel_id;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_visibility_check;
ALTER TABLE agent ADD CONSTRAINT agent_visibility_check
  CHECK (visibility IN ('workspace', 'private'));

ALTER TABLE agent_creation_draft DROP CONSTRAINT IF EXISTS agent_creation_draft_visibility_check;
ALTER TABLE agent_creation_draft ADD CONSTRAINT agent_creation_draft_visibility_check
  CHECK (visibility IN ('workspace', 'private'));
