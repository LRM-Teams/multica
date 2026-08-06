DROP INDEX IF EXISTS idx_channel_message_quote_message;

ALTER TABLE channel_message
  DROP COLUMN IF EXISTS quote_snapshot,
  DROP COLUMN IF EXISTS quote_message_id;
