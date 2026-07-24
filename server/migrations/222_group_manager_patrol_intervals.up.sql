BEGIN;

-- Product policy separates the agent's normal adaptive horizon from the
-- server's failure safety net:
--   * agent-chosen patrols: 15 minutes .. 8 hours
--   * server failure fallback: 12 hours
--
-- Migration 221 used 24 hours for both. Shorten only active managed patrols
-- that still exceed the new applicable cap. Ordinary user reminders are not
-- touched. The latest lifecycle reason tells a server-installed fallback from
-- an agent-authored replan, and every changed row gets its own audit event.
WITH candidates AS (
  SELECT
    reminder.id,
    reminder.fire_at AS previous_fire_at,
    CASE
      WHEN latest.reason_code IN (
        'patrol_failure_fallback_rearmed',
        'patrol_failure_fallback_cap_migrated'
      )
        THEN now() + interval '12 hours'
      ELSE now() + interval '8 hours'
    END AS capped_fire_at,
    CASE
      WHEN latest.reason_code IN (
        'patrol_failure_fallback_rearmed',
        'patrol_failure_fallback_cap_migrated'
      )
        THEN 'patrol_failure_fallback_cap_migrated'
      ELSE 'patrol_adaptive_cap_migrated'
    END AS migration_reason
  FROM agent_reminder reminder
  LEFT JOIN LATERAL (
    SELECT lifecycle.reason_code
    FROM agent_reminder_lifecycle_event lifecycle
    WHERE lifecycle.reminder_id = reminder.id
    ORDER BY lifecycle.created_at DESC, lifecycle.id DESC
    LIMIT 1
  ) latest ON true
  WHERE reminder.origin_kind = 'group_manager_auto'
    AND reminder.managed_kind = 'patrol'
    AND reminder.status = 'scheduled'
),
updated AS (
  UPDATE agent_reminder reminder
  SET fire_at = candidates.capped_fire_at,
      cadence = NULL,
      schedule_timezone = NULL,
      cadence_next_at = NULL,
      version = reminder.version + 1,
      updated_at = now()
  FROM candidates
  WHERE reminder.id = candidates.id
    AND reminder.fire_at > candidates.capped_fire_at
  RETURNING
    reminder.id,
    reminder.workspace_id,
    reminder.agent_id,
    reminder.title,
    reminder.fire_at,
    candidates.previous_fire_at,
    candidates.migration_reason
)
INSERT INTO agent_reminder_lifecycle_event (
  reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
  previous_fire_at, next_fire_at, title_snapshot, cadence_snapshot,
  resulting_state, reason_code, details
)
SELECT
  id, workspace_id, agent_id, 'snoozed', 'system', agent_id,
  previous_fire_at, fire_at, title, NULL,
  'scheduled', migration_reason,
  jsonb_build_object(
    'policy', 'group_manager_patrol_intervals_v2',
    'adaptive_max_seconds', 28800,
    'failure_fallback_seconds', 43200
  )
FROM updated;

COMMIT;
