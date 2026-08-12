-- Pending note writeback proposals (S1-W1 / D1).
-- Separate from note_ai_job: proposals are reviewed before note_page.content changes.

CREATE TABLE note_page_writeback (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('append', 'patch', 'replace_page')),
    content TEXT NOT NULL,
    -- Exact fragment to replace when action = patch; ignored otherwise.
    target TEXT,
    -- Non-empty array of {type, id, label?} evidence objects. Required by D1/S1-W1.
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'applied', 'rejected')),
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent')),
    created_by_id UUID NOT NULL,
    resolved_by UUID REFERENCES "user"(id),
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(evidence) = 'array'),
    CHECK (jsonb_array_length(evidence) >= 1),
    CHECK (
        (status = 'pending' AND resolved_by IS NULL AND resolved_at IS NULL)
        OR (status IN ('applied', 'rejected') AND resolved_at IS NOT NULL)
    ),
    CHECK (
        action <> 'patch'
        OR (target IS NOT NULL AND length(btrim(target)) > 0)
    )
);

CREATE INDEX note_page_writeback_page_status_idx
    ON note_page_writeback(page_id, status, created_at DESC);

CREATE INDEX note_page_writeback_workspace_status_idx
    ON note_page_writeback(workspace_id, status, created_at DESC);
