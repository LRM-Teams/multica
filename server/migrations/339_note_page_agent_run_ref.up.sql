-- Note → Agent / Run references (Slice 2 / S2-R1).
-- Keep separate tables (true FKs). Markdown mentions are not the source of truth.
-- Issue refs stay on note_page_issue_ref (S1-R1).

CREATE TABLE note_page_agent_ref (
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, agent_id)
);

CREATE INDEX note_page_agent_ref_agent_idx
    ON note_page_agent_ref(agent_id, page_id);

CREATE INDEX note_page_agent_ref_workspace_page_idx
    ON note_page_agent_ref(workspace_id, page_id, created_at);

-- run_id is agent_inbox_event.id (same id used in writeback evidence type=run).
CREATE TABLE note_page_run_ref (
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    run_id UUID NOT NULL REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, run_id)
);

CREATE INDEX note_page_run_ref_run_idx
    ON note_page_run_ref(run_id, page_id);

CREATE INDEX note_page_run_ref_agent_idx
    ON note_page_run_ref(agent_id, page_id);

CREATE INDEX note_page_run_ref_workspace_page_idx
    ON note_page_run_ref(workspace_id, page_id, created_at);
