UPDATE note_period_brief_run SET status = 'collecting' WHERE status = 'planning';

ALTER TABLE note_period_brief_run
    DROP CONSTRAINT IF EXISTS note_period_brief_run_status_check;

ALTER TABLE note_period_brief_run
    ADD CONSTRAINT note_period_brief_run_status_check
    CHECK (status IN ('collecting', 'synthesizing', 'done'));

ALTER TABLE note_period_brief_run
    DROP COLUMN IF EXISTS planner_job_id,
    DROP COLUMN IF EXISTS collect_plan,
    DROP COLUMN IF EXISTS user_focus;
