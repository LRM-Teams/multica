BEGIN;

-- Receipt links cannot be reconstructed: rollback restores only the legacy
-- schema. Existing canonical Messages and Reminder lifecycle history remain.
ALTER TABLE agent_reminder
  ADD COLUMN fired_receipt_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL;

CREATE INDEX idx_agent_reminder_fired_receipt
  ON agent_reminder(fired_receipt_message_id)
  WHERE fired_receipt_message_id IS NOT NULL;

ALTER TABLE agent_reminder_occurrence
  ADD COLUMN receipt_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  ADD COLUMN anchor_available BOOLEAN,
  DROP CONSTRAINT agent_reminder_occurrence_reminder_id_fire_version_key,
  DROP CONSTRAINT agent_reminder_occurrence_fire_version_check,
  DROP COLUMN fire_version,
  ADD CONSTRAINT agent_reminder_occurrence_reminder_id_cadence_scheduled_for_key
    UNIQUE (reminder_id, cadence_scheduled_for);

COMMIT;
