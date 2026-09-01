-- Persist where a finished 写汇报 landed so later Notes-bubble Q&A can
-- name the artifact without replaying collect/synthesis.

ALTER TABLE note_period_brief_run
    ADD COLUMN IF NOT EXISTS result_page_id UUID REFERENCES note_page(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS result_mode TEXT;

ALTER TABLE note_period_brief_run
    DROP CONSTRAINT IF EXISTS note_period_brief_run_result_mode_check;

ALTER TABLE note_period_brief_run
    ADD CONSTRAINT note_period_brief_run_result_mode_check
    CHECK (result_mode IS NULL OR result_mode IN ('append', 'child'));
