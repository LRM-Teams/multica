UPDATE note_period_brief_run SET status = 'done' WHERE status = 'cancelled';

ALTER TABLE note_period_brief_run
    DROP CONSTRAINT IF EXISTS note_period_brief_run_status_check;

ALTER TABLE note_period_brief_run
    ADD CONSTRAINT note_period_brief_run_status_check
    CHECK (status IN ('planning', 'collecting', 'synthesizing', 'awaiting_confirm', 'done'));
