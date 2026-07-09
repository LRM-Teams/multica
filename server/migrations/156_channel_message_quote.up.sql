ALTER TABLE channel_message
  ADD COLUMN IF NOT EXISTS quote_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS quote_snapshot JSONB;

CREATE INDEX IF NOT EXISTS idx_channel_message_quote
  ON channel_message (quote_message_id)
  WHERE quote_message_id IS NOT NULL;
