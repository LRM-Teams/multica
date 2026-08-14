-- Note → Channel collaboration anchors (todo N2-A1).
-- Links a product note to a Messages channel used as the collaboration field.
-- kind: worker (from Note Worker dispatch) | coordination (explicit / future).

CREATE TABLE note_page_channel_ref (
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT 'worker'
        CHECK (kind IN ('worker', 'coordination')),
    created_by UUID NOT NULL REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, channel_id)
);

CREATE INDEX note_page_channel_ref_channel_idx
    ON note_page_channel_ref(channel_id, page_id);

CREATE INDEX note_page_channel_ref_workspace_page_idx
    ON note_page_channel_ref(workspace_id, page_id, created_at);
