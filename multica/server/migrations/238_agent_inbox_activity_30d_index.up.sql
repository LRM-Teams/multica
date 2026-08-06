-- Hot path: GET /api/agent-activity-30d aggregates completed inbox events for a
-- workspace over 30 days. Without this, planner seq-scans agent_inbox_event
-- (~300k rows). Index + query filter on atq.workspace_id (not JOIN agent).
CREATE INDEX IF NOT EXISTS idx_agent_inbox_event_ws_completed_activity
  ON agent_inbox_event (workspace_id, completed_at DESC, agent_id)
  WHERE completed_at IS NOT NULL;
