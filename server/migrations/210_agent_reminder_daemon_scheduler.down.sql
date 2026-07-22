BEGIN;

DROP TRIGGER IF EXISTS agent_reminder_daemon_owner_event_trigger ON agent;
DROP FUNCTION IF EXISTS project_agent_reminder_daemon_owner_event();
DROP TRIGGER IF EXISTS agent_reminder_runtime_capability_trigger ON agent;
DROP FUNCTION IF EXISTS fence_agent_reminder_runtime_capability();
DROP TRIGGER IF EXISTS agent_runtime_reminder_capability_downgrade_trigger ON agent_runtime;
DROP FUNCTION IF EXISTS fence_agent_runtime_reminder_capability_downgrade();
DROP TRIGGER IF EXISTS agent_reminder_daemon_timer_event_trigger ON agent_reminder;
DROP FUNCTION IF EXISTS project_agent_reminder_daemon_timer_event();
DROP FUNCTION IF EXISTS enqueue_agent_reminder_daemon_projection(UUID, UUID, UUID, UUID, BIGINT, TEXT, BIGINT, TIMESTAMPTZ, BOOLEAN);
DROP TABLE IF EXISTS agent_reminder_daemon_projection_event;
DROP TABLE IF EXISTS agent_reminder_daemon_projection_cursor;
DROP TABLE IF EXISTS agent_reminder_daemon_owner_cursor;
DROP TABLE IF EXISTS agent_reminder_daemon_owner_event;
DROP SEQUENCE IF EXISTS agent_reminder_placement_generation_seq;

ALTER TABLE agent_reminder
  DROP COLUMN IF EXISTS version;

COMMIT;
