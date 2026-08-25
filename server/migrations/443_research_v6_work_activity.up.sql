CREATE TABLE research_work_item_activity_entry (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    work_item_id UUID NOT NULL,
    work_item_attempt_id UUID NOT NULL,
    inbox_task_id UUID NOT NULL REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
    message_sequence INTEGER NOT NULL CHECK (message_sequence > 0),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 240),
    subtext TEXT NOT NULL DEFAULT '' CHECK (length(subtext) <= 4000),
    tone TEXT NOT NULL CHECK (length(tone) BETWEEN 1 AND 40),
    body_kind TEXT NOT NULL CHECK (length(body_kind) BETWEEN 1 AND 40),
    body TEXT NOT NULL DEFAULT '' CHECK (length(body) <= 4000),
    observed_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (work_item_attempt_id, message_sequence),
    CONSTRAINT research_v6_work_activity_attempt_fk
        FOREIGN KEY (workspace_id, session_id, work_item_attempt_id)
        REFERENCES research_work_item_attempt(workspace_id, session_id, id)
        ON DELETE CASCADE
);

CREATE INDEX research_v6_work_activity_inbox_idx
    ON research_work_item_activity_entry(inbox_task_id, message_sequence DESC);
