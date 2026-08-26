DROP INDEX IF EXISTS note_page_share_unread_idx;

ALTER TABLE note_page_share
    DROP COLUMN IF EXISTS seen_at;
