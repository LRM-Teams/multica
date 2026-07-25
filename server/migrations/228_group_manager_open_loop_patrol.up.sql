BEGIN;

-- The open-loop patrol is deliberately application-driven. Remove the old
-- issue-state reset machine in one cutover; channel-message finalization now
-- re-arms only dormant patrols, while the group manager judges all candidate
-- evidence in its prompt.
DROP TRIGGER IF EXISTS channel_project_group_manager_patrol_scope_trigger ON channel;
DROP TRIGGER IF EXISTS issue_source_group_manager_patrol_scope_trigger ON issue_source_message;
DROP TRIGGER IF EXISTS comment_group_manager_patrol_progress_trigger ON comment;
DROP TRIGGER IF EXISTS issue_group_manager_patrol_progress_trigger ON issue;
DO $$
BEGIN
  -- Production is post-223 and uses agent_inbox_event. Keeping the guarded
  -- legacy branch makes the cutover composable with the 223 rollback test
  -- without retaining either trigger in a real migrated schema.
  IF to_regclass('agent_inbox_event') IS NOT NULL THEN
    EXECUTE 'DROP TRIGGER IF EXISTS task_group_manager_patrol_progress_trigger ON agent_inbox_event';
  END IF;
  IF to_regclass('agent_task_queue') IS NOT NULL THEN
    EXECUTE 'DROP TRIGGER IF EXISTS task_group_manager_patrol_progress_trigger ON agent_task_queue';
  END IF;
END;
$$;

DROP FUNCTION IF EXISTS refresh_group_manager_patrol_from_channel_project();
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_from_source();
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_from_issue_child();
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_for_issue();
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_for_issue_row(issue);
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_for_channel(UUID, UUID, TEXT);
DROP FUNCTION IF EXISTS group_manager_patrol_channel_has_active_issue(UUID, UUID);

-- The open-loop prompt reads a manager's most recent outbound DMs to avoid
-- repeating an unchanged reminder. Keep that bounded lookup independent of
-- total channel_message volume.
CREATE INDEX IF NOT EXISTS idx_channel_message_agent_outbound_recent
  ON channel_message(workspace_id, author_id, created_at DESC, id DESC)
  WHERE author_type = 'agent' AND deleted_at IS NULL;

-- Every canonical group/thread message checks whether its manager patrol is
-- dormant. Keep that hot-path lookup bounded as reminder history accumulates.
CREATE INDEX IF NOT EXISTS idx_agent_reminder_group_manager_dormant_patrol
  ON agent_reminder(workspace_id, anchor_channel_id, id)
  WHERE origin_kind = 'group_manager_auto'
    AND managed_kind = 'patrol'
    AND status = 'fired';

-- Give every existing dormant patrol one bounded open-loop evaluation without
-- replacing its durable definition. Cancelled patrols are explicit human
-- choices and remain cancelled.
WITH targets AS MATERIALIZED (
  SELECT reminder.id, reminder.status AS previous_status,
         reminder.fire_at AS previous_fire_at
  FROM agent_reminder reminder
  WHERE reminder.origin_kind = 'group_manager_auto'
    AND reminder.managed_kind = 'patrol'
    AND reminder.status IN ('scheduled', 'fired')
    AND NOT EXISTS (
      SELECT 1
      FROM agent_reminder_lifecycle_event lifecycle
      WHERE lifecycle.reminder_id = reminder.id
        AND lifecycle.reason_code = 'patrol_open_loop_policy_migrated'
    )
  FOR UPDATE
),
rearmed AS (
  UPDATE agent_reminder reminder
  SET status = 'scheduled',
      fire_at = now() + interval '15 minutes',
      cadence = NULL,
      schedule_timezone = NULL,
      cadence_next_at = NULL,
      current_occurrence_id = NULL,
      terminal_reason = NULL,
      fired_task_id = NULL,
      managed_backoff_step = 0,
      version = reminder.version + 1,
      updated_at = now()
  FROM targets
  WHERE reminder.id = targets.id
    AND targets.previous_status = 'fired'
  RETURNING reminder.id
)
INSERT INTO agent_reminder_lifecycle_event (
  reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
  previous_fire_at, next_fire_at, title_snapshot, cadence_snapshot,
  timezone_snapshot, resulting_state, reason_code, details
)
SELECT
  reminder.id, reminder.workspace_id, reminder.agent_id,
  'updated', 'system', reminder.agent_id,
  targets.previous_fire_at, reminder.fire_at, reminder.title,
  reminder.cadence, reminder.schedule_timezone, reminder.status,
  'patrol_open_loop_policy_migrated',
  jsonb_build_object(
    'policy', 'group_manager_open_loop_v1',
    'previous_status', targets.previous_status,
    'dormant_rearmed', targets.previous_status = 'fired'
  )
FROM targets
JOIN agent_reminder reminder ON reminder.id = targets.id;

COMMIT;
