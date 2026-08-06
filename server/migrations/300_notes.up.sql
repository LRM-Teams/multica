CREATE TABLE note_page (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    parent_id UUID REFERENCES note_page(id) ON DELETE CASCADE,
    owner_user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT 'Untitled',
    content TEXT NOT NULL DEFAULT '',
    sort_key TEXT NOT NULL DEFAULT '',
    created_by UUID NOT NULL REFERENCES "user"(id),
    updated_by UUID NOT NULL REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CHECK (parent_id IS NULL OR parent_id <> id)
);

CREATE TABLE note_page_share (
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, user_id)
);

CREATE INDEX note_page_workspace_parent_idx
    ON note_page(workspace_id, parent_id, sort_key, created_at)
    WHERE deleted_at IS NULL;

CREATE INDEX note_page_workspace_owner_idx
    ON note_page(workspace_id, owner_user_id)
    WHERE deleted_at IS NULL;

CREATE INDEX note_page_share_user_idx
    ON note_page_share(user_id, page_id);
