BEGIN;

-- Migration 208 rollback: restore the V1 one-shot Reminder shape.

ALTER TABLE agent_reminder
  DROP CONSTRAINT IF EXISTS agent_reminder_current_occurrence_fkey;

DROP TABLE IF EXISTS agent_reminder_lifecycle_event;
DROP TABLE IF EXISTS agent_reminder_occurrence;

ALTER TABLE agent_reminder
  DROP CONSTRAINT IF EXISTS agent_reminder_cadence_timezone_check,
  DROP CONSTRAINT IF EXISTS agent_reminder_cadence_shape_check,
  DROP COLUMN IF EXISTS terminal_reason,
  DROP COLUMN IF EXISTS current_occurrence_id,
  DROP COLUMN IF EXISTS cadence_next_at,
  DROP COLUMN IF EXISTS schedule_timezone,
  DROP COLUMN IF EXISTS cadence,
  DROP COLUMN IF EXISTS initiator_user_id;

-- V1 used cascading ownership FKs.  Restore them for an exact schema rollback.
DELETE FROM agent_reminder reminder
WHERE NOT EXISTS (SELECT 1 FROM agent WHERE agent.id = reminder.agent_id)
   OR NOT EXISTS (SELECT 1 FROM channel WHERE channel.id = reminder.anchor_channel_id);

ALTER TABLE agent_reminder
  ADD CONSTRAINT agent_reminder_agent_id_fkey
    FOREIGN KEY (agent_id) REFERENCES agent(id) ON DELETE CASCADE,
  ADD CONSTRAINT agent_reminder_anchor_channel_id_fkey
    FOREIGN KEY (anchor_channel_id) REFERENCES channel(id) ON DELETE CASCADE;

COMMIT;
