-- LRM-769: channel notification preference four-state (default/all/mentions/muted).
-- Renumbered from 249 → 250 after 249_workspace_onboarding_agent landed on dev first.
-- Idempotent: safe if an environment already applied the old 249_channel_member_notify_level stem.
-- NULL notify_level means "default" (never stored as the literal 'default').
-- Backfill: prior mute (muted_at set) maps to mentions (legacy mute = @-only).

ALTER TABLE channel_member
  ADD COLUMN IF NOT EXISTS notify_level TEXT;

UPDATE channel_member
SET notify_level = 'mentions'
WHERE muted_at IS NOT NULL
  AND notify_level IS NULL;

ALTER TABLE channel_member
  DROP CONSTRAINT IF EXISTS channel_member_notify_level_check;

ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_notify_level_check
  CHECK (notify_level IS NULL OR notify_level IN ('all', 'mentions', 'muted'));
