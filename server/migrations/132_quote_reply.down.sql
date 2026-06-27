DROP INDEX IF EXISTS idx_channel_message_reply_to;

ALTER TABLE channel_message
    DROP COLUMN IF EXISTS reply_to_message_id;
