ALTER TABLE channel_message
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS edited_at;
