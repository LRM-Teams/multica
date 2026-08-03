DROP TABLE IF EXISTS research_scheduler_event;
DROP TABLE IF EXISTS research_work_item;

ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_max_open_branches_check;

ALTER TABLE research_session
  DROP COLUMN IF EXISTS last_user_activity_at,
  DROP COLUMN IF EXISTS unattended_auto_steps,
  DROP COLUMN IF EXISTS single_line_confirmed,
  DROP COLUMN IF EXISTS max_open_branches,
  DROP COLUMN IF EXISTS unattended_enabled;
