BEGIN;

-- Managed Patrol was a retired server-owned Reminder subtype. Channel Manager
-- responsibility now only supplies durable role context; every surviving
-- Reminder is an ordinary, self-owned Agent Reminder.
DELETE FROM agent_reminder
WHERE origin_kind <> 'agent'
   OR managed_kind IS NOT NULL
   OR origin_key IS NOT NULL;

DROP INDEX IF EXISTS agent_reminder_active_managed_patrol_uidx;
DROP INDEX IF EXISTS idx_agent_reminder_group_manager_dormant_patrol;

ALTER TABLE agent_reminder
    DROP CONSTRAINT IF EXISTS agent_reminder_origin_kind_check,
    DROP CONSTRAINT IF EXISTS agent_reminder_managed_kind_check,
    DROP CONSTRAINT IF EXISTS agent_reminder_managed_origin_check,
    DROP CONSTRAINT IF EXISTS agent_reminder_managed_backoff_step_check,
    DROP COLUMN IF EXISTS origin_kind,
    DROP COLUMN IF EXISTS managed_kind,
    DROP COLUMN IF EXISTS origin_key,
    DROP COLUMN IF EXISTS managed_backoff_step;

COMMIT;
