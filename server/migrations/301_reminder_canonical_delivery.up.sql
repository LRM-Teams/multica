BEGIN;

-- Reminder fire is a canonical Message delivery, not an inbox task. Existing
-- fired task references cannot be reinterpreted as message ids, so retain the
-- lifecycle history and remove only the obsolete transport linkage.
DROP INDEX IF EXISTS idx_agent_reminder_occurrence_fired_task;
DROP INDEX IF EXISTS idx_agent_reminder_fired_task;

ALTER TABLE agent_reminder
  ADD COLUMN fired_receipt_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL;

ALTER TABLE agent_reminder
  DROP COLUMN fired_task_id;

ALTER TABLE agent_reminder_occurrence
  DROP COLUMN fired_task_id;

CREATE INDEX idx_agent_reminder_fired_receipt
  ON agent_reminder(fired_receipt_message_id)
  WHERE fired_receipt_message_id IS NOT NULL;

COMMIT;
