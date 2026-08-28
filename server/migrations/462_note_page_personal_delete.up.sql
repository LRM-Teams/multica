CREATE TABLE note_page_hidden (
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, user_id)
);

CREATE INDEX note_page_hidden_user_idx
    ON note_page_hidden(user_id, page_id);

ALTER TABLE note_page
    DROP CONSTRAINT IF EXISTS note_page_parent_id_fkey,
    ADD CONSTRAINT note_page_parent_id_fkey
        FOREIGN KEY (parent_id) REFERENCES note_page(id) ON DELETE SET NULL;
