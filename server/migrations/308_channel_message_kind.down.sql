-- Rollback LRM-1523 L1: remove the structured channel_message.kind classification.
DROP INDEX IF EXISTS idx_channel_message_kind_channel_created;
ALTER TABLE channel_message
    DROP COLUMN IF EXISTS kind;
