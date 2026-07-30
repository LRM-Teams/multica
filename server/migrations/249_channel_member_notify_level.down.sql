ALTER TABLE channel_member
  DROP CONSTRAINT IF EXISTS channel_member_notify_level_check;

ALTER TABLE channel_member
  DROP COLUMN IF EXISTS notify_level;
