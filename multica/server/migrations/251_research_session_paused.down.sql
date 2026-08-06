-- Revert paused sessions before restoring the old CHECK.
UPDATE research_session SET status = 'archived', updated_at = now()
WHERE status = 'paused';

ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_status_check;

ALTER TABLE research_session
  ADD CONSTRAINT research_session_status_check
  CHECK (status IN (
    'drafting',
    'running',
    'awaiting_user_confirm',
    'completed',
    'archived'
  ));
