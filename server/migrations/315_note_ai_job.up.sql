CREATE TABLE note_ai_job (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    creator_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    chat_session_id UUID NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
    prompt TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id)
);

CREATE INDEX note_ai_job_workspace_creator_idx
    ON note_ai_job(workspace_id, creator_id, created_at DESC);

CREATE INDEX note_ai_job_chat_session_idx
    ON note_ai_job(chat_session_id);
