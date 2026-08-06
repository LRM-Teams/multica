ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_terminal_outcome_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_terminal_outcome_check
  CHECK (terminal_outcome IN ('replied', 'held', 'no_reply', 'failed'));
