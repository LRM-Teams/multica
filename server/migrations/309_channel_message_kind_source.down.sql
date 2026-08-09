-- Rollback LRM-1529 kind_source + expanded kind CHECK.

DROP INDEX IF EXISTS idx_channel_message_kind_source_created;

-- Rows using kinds beyond the LRM-1523 set must be normalized before the
-- narrower CHECK can be restored.
UPDATE channel_message
SET kind = 'content'
WHERE kind IN ('handoff', 'delegation', 'review', 'deliverable');

ALTER TABLE channel_message
    DROP CONSTRAINT IF EXISTS channel_message_kind_check;

ALTER TABLE channel_message
    ADD CONSTRAINT channel_message_kind_check
        CHECK (kind IN ('content', 'confirmation', 'status', 'system_reminder'));

ALTER TABLE channel_message
    DROP COLUMN IF EXISTS kind_source;
