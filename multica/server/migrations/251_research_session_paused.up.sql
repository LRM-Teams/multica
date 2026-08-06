-- Allow research sessions to be paused (stop) without deleting them.
ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_status_check;

ALTER TABLE research_session
  ADD CONSTRAINT research_session_status_check
  CHECK (status IN (
    'drafting',
    'running',
    'awaiting_user_confirm',
    'completed',
    'archived',
    'paused'
  ));
