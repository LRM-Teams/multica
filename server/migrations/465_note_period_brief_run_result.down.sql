ALTER TABLE note_period_brief_run
    DROP CONSTRAINT IF EXISTS note_period_brief_run_result_mode_check;

ALTER TABLE note_period_brief_run
    DROP COLUMN IF EXISTS result_mode,
    DROP COLUMN IF EXISTS result_page_id;
