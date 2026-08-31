ALTER TABLE note_page_share
    ADD COLUMN IF NOT EXISTS seen_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS note_page_share_unread_idx
    ON note_page_share(user_id, page_id)
    WHERE seen_at IS NULL;
