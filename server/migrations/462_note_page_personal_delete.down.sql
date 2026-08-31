ALTER TABLE note_page
    DROP CONSTRAINT IF EXISTS note_page_parent_id_fkey,
    ADD CONSTRAINT note_page_parent_id_fkey
        FOREIGN KEY (parent_id) REFERENCES note_page(id) ON DELETE CASCADE;

DROP INDEX IF EXISTS note_page_hidden_user_idx;
DROP TABLE IF EXISTS note_page_hidden;
