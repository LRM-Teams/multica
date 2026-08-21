-- Remap statuses introduced by the bubble flow before narrowing the CHECK.
UPDATE note_period_brief_run
SET status = 'synthesizing'
WHERE status = 'awaiting_confirm';

ALTER TABLE note_period_brief_run
    DROP CONSTRAINT IF EXISTS note_period_brief_run_status_check;

ALTER TABLE note_period_brief_run
    ADD CONSTRAINT note_period_brief_run_status_check
    CHECK (status IN ('planning', 'collecting', 'synthesizing', 'done'));

DROP INDEX IF EXISTS note_period_brief_run_chat_session_idx;
DROP INDEX IF EXISTS note_period_brief_run_source_page_idx;

ALTER TABLE note_period_brief_run
    DROP COLUMN IF EXISTS source_page_id,
    DROP COLUMN IF EXISTS chat_session_id;
