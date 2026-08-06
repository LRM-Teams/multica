ALTER TABLE channel_message
    ADD COLUMN IF NOT EXISTS reply_to_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_channel_message_reply_to
    ON channel_message (reply_to_message_id)
    WHERE reply_to_message_id IS NOT NULL;
