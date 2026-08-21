-- Notes Assistant collect-plan: optional human focus + planner artifact.
-- Empty user_focus keeps today's full-scope collect. Non-empty wakes 笔记助手
-- to assign scoped collector tasks before harvest.

ALTER TABLE note_period_brief_run
    ADD COLUMN IF NOT EXISTS user_focus TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS collect_plan JSONB,
    ADD COLUMN IF NOT EXISTS planner_job_id UUID;

ALTER TABLE note_period_brief_run
    DROP CONSTRAINT IF EXISTS note_period_brief_run_status_check;

ALTER TABLE note_period_brief_run
    ADD CONSTRAINT note_period_brief_run_status_check
    CHECK (status IN ('planning', 'collecting', 'synthesizing', 'done'));
