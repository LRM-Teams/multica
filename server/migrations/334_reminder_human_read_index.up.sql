-- The migration runner executes this multi-index file as one batch, so use
-- regular DDL. Apply in the server deployment window: each index briefly
-- locks writes to its Reminder table while PostgreSQL builds it.
CREATE INDEX idx_agent_reminder_human_upcoming
  ON agent_reminder(workspace_id, agent_id, fire_at ASC, id ASC)
  WHERE status IN ('scheduled', 'firing');

CREATE INDEX idx_agent_reminder_occurrence_human_history
  ON agent_reminder_occurrence(workspace_id, agent_id, fired_at DESC, id DESC)
  WHERE status = 'fired';
