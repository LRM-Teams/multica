ALTER TABLE note_page_share
    ADD COLUMN seen_at TIMESTAMPTZ;

CREATE INDEX note_page_share_unread_idx
    ON note_page_share(user_id, page_id)
    WHERE seen_at IS NULL;
