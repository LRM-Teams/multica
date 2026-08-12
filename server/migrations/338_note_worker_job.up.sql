-- Note Worker jobs (S2-C3): platform work briefed by a note page.
-- Separate from note_ai_job (Editor). Must never share rows or endpoints.

CREATE TABLE note_worker_job (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    creator_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    -- Trusted user directive. Note page content is loaded under ACL at dispatch (S2-C1), not copied here.
    instruction TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'dispatched', 'running', 'completed', 'failed', 'cancelled')),
    -- Populated when S2-C1 wires platform dispatch; NULL while only the contract is recorded.
    task_id UUID REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (length(btrim(instruction)) > 0)
);

CREATE INDEX note_worker_job_workspace_creator_idx
    ON note_worker_job(workspace_id, creator_id, created_at DESC);

CREATE INDEX note_worker_job_page_idx
    ON note_worker_job(page_id, created_at DESC);

CREATE INDEX note_worker_job_agent_idx
    ON note_worker_job(agent_id);
