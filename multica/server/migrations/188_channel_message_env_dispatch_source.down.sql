-- Re-tighten channel_message.source to the pre-188 ('multica', 'lark') set.
-- Existing 'env_dispatch' rows are rewritten to the default 'multica' first so
-- the stricter CHECK does not reject them on re-add (and no messages are dropped,
-- which could orphan quote/source_message_id references).
UPDATE channel_message SET source = 'multica' WHERE source = 'env_dispatch';

ALTER TABLE channel_message
    DROP CONSTRAINT IF EXISTS channel_message_source_check,
    ADD CONSTRAINT channel_message_source_check
        CHECK (source IN ('multica', 'lark'));
