ALTER TABLE wendy_channel_ambient
  DROP COLUMN IF EXISTS reviewing_message_at,
  DROP COLUMN IF EXISTS first_dirty_message_at;
