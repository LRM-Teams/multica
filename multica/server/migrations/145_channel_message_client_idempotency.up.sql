ALTER TABLE channel_message
  ADD COLUMN IF NOT EXISTS client_message_id TEXT;

ALTER TABLE channel_message
  ADD CONSTRAINT channel_message_client_message_id_len
  CHECK (client_message_id IS NULL OR char_length(client_message_id) <= 128);

CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_message_client_idempotency
  ON channel_message (workspace_id, channel_id, author_id, client_message_id)
  WHERE author_type = 'user' AND client_message_id IS NOT NULL;
