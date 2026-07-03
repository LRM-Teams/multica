DROP INDEX IF EXISTS idx_channel_message_client_idempotency;

ALTER TABLE channel_message
  DROP CONSTRAINT IF EXISTS channel_message_client_message_id_len;

ALTER TABLE channel_message
  DROP COLUMN IF EXISTS client_message_id;
