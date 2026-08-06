BEGIN;

DROP INDEX IF EXISTS idx_agent_reminder_fired_receipt;

ALTER TABLE agent_reminder
  DROP COLUMN fired_receipt_message_id;

ALTER TABLE agent_reminder
  ADD COLUMN fired_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL;

ALTER TABLE agent_reminder_occurrence
  ADD COLUMN fired_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL;

CREATE INDEX idx_agent_reminder_fired_task
  ON agent_reminder(fired_task_id)
  WHERE fired_task_id IS NOT NULL;

CREATE INDEX idx_agent_reminder_occurrence_fired_task
  ON agent_reminder_occurrence(fired_task_id)
  WHERE fired_task_id IS NOT NULL;

COMMIT;
