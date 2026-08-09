-- LRM-1523 L1 DB-layer split: reserve the "confirmation" / "status" /
-- system_reminder message classes as structured channel_message.kind values so
-- the echo-suppression is enforced structurally (not only by the runtime text
-- classifier). A "confirmation" message (acknowledgement with no new
-- information, no @-directive, no action) must not wake any agent; "status"
-- and "system_reminder" carry the same no-wake / observe-only semantics.
--
-- Non-destructive: the column defaults to 'content' so every existing row is
-- backfilled as ordinary content and all legacy writers keep working. Rollback
-- simply drops the column.

ALTER TABLE channel_message
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'content'
        CHECK (kind IN ('content', 'confirmation', 'status', 'system_reminder'));

-- Help the dispatch path answer "how many confirmations does this channel have
-- today" and index the classification without scanning every row.
CREATE INDEX idx_channel_message_kind_channel_created
    ON channel_message(channel_id, kind, created_at)
    WHERE kind <> 'content';
