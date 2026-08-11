BEGIN;

-- A fire is accepted by the Reminder definition version, not by a visible
-- Message receipt or by a cadence timestamp. Historical rows receive a
-- negative identity so they remain queryable without colliding with future
-- positive definition versions.
ALTER TABLE agent_reminder_occurrence
  ADD COLUMN fire_version BIGINT;

WITH historical AS (
  SELECT id,
         -row_number() OVER (
           PARTITION BY reminder_id
           ORDER BY cadence_scheduled_for, created_at, id
         ) AS fire_version
  FROM agent_reminder_occurrence
)
UPDATE agent_reminder_occurrence occurrence
SET fire_version = historical.fire_version
FROM historical
WHERE historical.id = occurrence.id;

ALTER TABLE agent_reminder_occurrence
  ALTER COLUMN fire_version SET NOT NULL,
  ADD CONSTRAINT agent_reminder_occurrence_fire_version_check
    CHECK (fire_version <> 0),
  DROP CONSTRAINT agent_reminder_occurrence_reminder_id_cadence_scheduled_for_key,
  ADD CONSTRAINT agent_reminder_occurrence_reminder_id_fire_version_key
    UNIQUE (reminder_id, fire_version),
  DROP COLUMN receipt_message_id,
  DROP COLUMN anchor_available;

DROP INDEX IF EXISTS idx_agent_reminder_fired_receipt;

ALTER TABLE agent_reminder
  DROP COLUMN fired_receipt_message_id;

COMMIT;
