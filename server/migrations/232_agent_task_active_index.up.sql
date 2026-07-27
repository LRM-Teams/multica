-- Presence reads pending, draining, and failed rows together. The older ready
-- index intentionally excludes draining rows and therefore cannot satisfy the
-- combined predicate. CONCURRENTLY must remain the only statement here.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_inbox_event_workspace_active
  ON agent_inbox_event(workspace_id, agent_id, status, created_at DESC, id DESC)
  WHERE status IN ('pending', 'draining', 'failed');
