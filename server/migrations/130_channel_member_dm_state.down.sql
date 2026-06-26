ALTER TABLE channel_member
  DROP COLUMN IF EXISTS hidden_at,
  DROP COLUMN IF EXISTS pinned_at;
