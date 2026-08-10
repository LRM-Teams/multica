-- LRM-1529: observe how channel_message.kind was derived so lexicon fallback
-- can be measured and retired. Priority is structured → system → lexicon → default.
-- Also expands the kind CHECK to the Agent Coordination Protocol v1 set.

ALTER TABLE channel_message
    ADD COLUMN kind_source TEXT NOT NULL DEFAULT 'default'
        CHECK (kind_source IN ('structured', 'system', 'lexicon', 'default'));

ALTER TABLE channel_message
    DROP CONSTRAINT IF EXISTS channel_message_kind_check;

ALTER TABLE channel_message
    ADD CONSTRAINT channel_message_kind_check
        CHECK (kind IN (
            'content',
            'confirmation',
            'status',
            'handoff',
            'delegation',
            'review',
            'deliverable',
            'system_reminder'
        ));

CREATE INDEX idx_channel_message_kind_source_created
    ON channel_message(kind_source, created_at)
    WHERE kind_source <> 'default';
