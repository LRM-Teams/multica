-- Human stop on an in-flight 写汇报 run. Composer lock ends when status
-- leaves planning/collecting/synthesizing; cancelled is a terminal stop.

ALTER TABLE note_period_brief_run
    DROP CONSTRAINT IF EXISTS note_period_brief_run_status_check;

ALTER TABLE note_period_brief_run
    ADD CONSTRAINT note_period_brief_run_status_check
    CHECK (status IN ('planning', 'collecting', 'synthesizing', 'awaiting_confirm', 'done', 'cancelled'));
