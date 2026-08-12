-- Note → Issue references (Slice 1 / S1-R1).
-- Stable queryable links; Markdown mentions alone are not the source of truth.
-- Slice 1 is note→issue only. Issue→note reverse discovery is Slice 3.

CREATE TABLE note_page_issue_ref (
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, issue_id)
);

CREATE INDEX note_page_issue_ref_issue_idx
    ON note_page_issue_ref(issue_id, page_id);

CREATE INDEX note_page_issue_ref_workspace_page_idx
    ON note_page_issue_ref(workspace_id, page_id, created_at);
