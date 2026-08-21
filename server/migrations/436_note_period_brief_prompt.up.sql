-- Notes-bubble intake: ask for time + computers before starting collectors.

CREATE TABLE IF NOT EXISTS note_period_brief_prompt (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    owner_user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    chat_session_id UUID NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    source_page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    window_kind TEXT NOT NULL DEFAULT '',
    window_date TEXT NOT NULL DEFAULT '',
    start_date TEXT NOT NULL DEFAULT '',
    end_date TEXT NOT NULL DEFAULT '',
    collector_agent_ids TEXT[] NOT NULL DEFAULT '{}',
    focus TEXT NOT NULL DEFAULT '',
    awaiting_confirm BOOLEAN NOT NULL DEFAULT FALSE,
    status TEXT NOT NULL DEFAULT 'clarifying',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT note_period_brief_prompt_status_check
        CHECK (status IN ('clarifying', 'consumed', 'cancelled'))
);

CREATE UNIQUE INDEX IF NOT EXISTS note_period_brief_prompt_active_session_idx
    ON note_period_brief_prompt (chat_session_id)
    WHERE status = 'clarifying';
