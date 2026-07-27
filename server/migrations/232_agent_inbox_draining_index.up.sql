-- Drain is polled once per online runtime every two seconds. Keep the
-- status='draining' candidate set index-sized so an empty reclaim does not
-- scan the full inbox_event history on every poll. CONCURRENTLY must remain
-- the only statement in this migration file.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_inbox_event_draining_runtime
  ON agent_inbox_event(runtime_id, agent_session_id, id)
  WHERE status = 'draining';
