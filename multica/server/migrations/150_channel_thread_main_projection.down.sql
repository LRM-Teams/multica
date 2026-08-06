DROP INDEX IF EXISTS idx_channel_message_main_projection_seq;

ALTER TABLE channel_message
  DROP COLUMN IF EXISTS main_timeline_visible;
