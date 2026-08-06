ALTER TABLE attachment
  ADD COLUMN channel_id UUID REFERENCES channel(id) ON DELETE CASCADE,
  ADD COLUMN channel_message_id UUID REFERENCES channel_message(id) ON DELETE CASCADE;

CREATE INDEX idx_attachment_channel
  ON attachment(channel_id)
  WHERE channel_id IS NOT NULL;

CREATE INDEX idx_attachment_channel_message
  ON attachment(channel_message_id)
  WHERE channel_message_id IS NOT NULL;
