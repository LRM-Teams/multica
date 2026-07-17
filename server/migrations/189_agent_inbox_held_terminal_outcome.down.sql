-- Older binaries cannot represent a persisted held draft outcome. Preserve
-- their closest non-failure state only for an explicit rollback.
UPDATE agent_inbox_event
SET terminal_outcome = 'no_reply'
WHERE terminal_outcome = 'held';

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_terminal_outcome_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_terminal_outcome_check
  CHECK (terminal_outcome IN ('replied', 'no_reply', 'failed'));
