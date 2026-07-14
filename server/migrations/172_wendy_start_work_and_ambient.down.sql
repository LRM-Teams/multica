DROP INDEX IF EXISTS wendy_channel_ambient_workspace_idx;
DROP INDEX IF EXISTS wendy_channel_ambient_due_idx;
DROP TABLE IF EXISTS wendy_channel_ambient;

ALTER TABLE pending_handoff
  DROP CONSTRAINT IF EXISTS pending_handoff_reason_code_check;

ALTER TABLE pending_handoff
  ADD CONSTRAINT pending_handoff_reason_code_check
  CHECK (reason_code IN (
    'unlock',
    'block_route',
    'interrupt_stop',
    'stalled_ask_why',
    'progress_nudge'
  ));
