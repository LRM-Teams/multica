-- The workspace presence snapshot needs one latest completed/failed outcome
-- per agent. Match its workspace + DISTINCT ON ordering so the read stays
-- proportional to the number of agents rather than the inbox history.
-- CONCURRENTLY must remain the only statement in this migration file.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_inbox_event_workspace_agent_outcome
  ON agent_inbox_event(
    workspace_id,
    agent_id,
    completed_at DESC NULLS LAST,
    id DESC
  )
  WHERE status = 'acked'
    AND terminal_outcome IN ('completed', 'failed');
