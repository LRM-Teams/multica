-- Bind a Period Brief run to the Notes bubble chat session and the page
-- the human started from, so progress lives in that transcript.

ALTER TABLE note_period_brief_run
    ADD COLUMN IF NOT EXISTS chat_session_id UUID REFERENCES chat_session(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS source_page_id UUID REFERENCES note_page(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS note_period_brief_run_source_page_idx
    ON note_period_brief_run (workspace_id, source_page_id, created_at DESC)
    WHERE source_page_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS note_period_brief_run_chat_session_idx
    ON note_period_brief_run (chat_session_id, created_at DESC)
    WHERE chat_session_id IS NOT NULL;

ALTER TABLE note_period_brief_run
    DROP CONSTRAINT IF EXISTS note_period_brief_run_status_check;

ALTER TABLE note_period_brief_run
    ADD CONSTRAINT note_period_brief_run_status_check
    CHECK (status IN ('planning', 'collecting', 'synthesizing', 'awaiting_confirm', 'done'));
