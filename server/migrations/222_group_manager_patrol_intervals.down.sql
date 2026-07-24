BEGIN;

DROP TRIGGER IF EXISTS channel_project_group_manager_patrol_scope_trigger ON channel;
DROP TRIGGER IF EXISTS issue_source_group_manager_patrol_scope_trigger ON issue_source_message;
DROP TRIGGER IF EXISTS task_group_manager_patrol_progress_trigger ON agent_task_queue;
DROP TRIGGER IF EXISTS comment_group_manager_patrol_progress_trigger ON comment;
DROP TRIGGER IF EXISTS issue_group_manager_patrol_progress_trigger ON issue;

DROP FUNCTION IF EXISTS refresh_group_manager_patrol_from_channel_project();
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_from_source();
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_from_issue_child();
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_for_issue();
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_for_issue_row(issue);
DROP FUNCTION IF EXISTS refresh_group_manager_patrol_for_channel(UUID, UUID, TEXT);
DROP FUNCTION IF EXISTS group_manager_patrol_channel_has_active_issue(UUID, UUID);

ALTER TABLE agent_reminder
  DROP CONSTRAINT IF EXISTS agent_reminder_managed_backoff_step_check,
  DROP COLUMN IF EXISTS managed_backoff_step;

COMMIT;
