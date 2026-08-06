-- Task #908 (agent visibility mechanism retirement), channel-scoped-agent cut:
-- visibility='channel' + home_channel_id existed solely to bind the Beckham
-- (贝克汉姆) group-manager agent to its home group (LRM-370/LRM-240). Beckham
-- was itself retired the day before this migration, in #1436 ("cut over
-- group managers to channel roles") — group management is now a channel_member
-- role, not a separate agent bound via visibility. No live code path has set
-- visibility='channel' since #1436 merged; this migration removes the
-- now-dead mechanism entirely.
--
-- Any legacy visibility='channel' rows (pre-#1436 data, never backfilled)
-- are converted to workspace, not private, with home_channel_id cleared
-- before the column and CHECK constraints that reference it are dropped.
-- Frank's #908 ruling is "default public" (#multica thread f83df812,
-- 2026-07-30 10:55-11:01) — converting to private would silently apply the
-- strictest visibility to rows nobody asked to lock down (Parker, same
-- thread, 18:31).

UPDATE agent
SET visibility = 'workspace',
    home_channel_id = NULL,
    updated_at = now()
WHERE visibility = 'channel';

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_home_channel_visibility_check;

DROP INDEX IF EXISTS agent_home_channel_id_idx;

ALTER TABLE agent DROP COLUMN IF EXISTS home_channel_id;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_visibility_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_visibility_check
  CHECK (visibility IN ('workspace', 'private'));

ALTER TABLE agent_creation_draft DROP CONSTRAINT IF EXISTS agent_creation_draft_visibility_check;
ALTER TABLE agent_creation_draft
  ADD CONSTRAINT agent_creation_draft_visibility_check
  CHECK (visibility IN ('workspace', 'private'));
