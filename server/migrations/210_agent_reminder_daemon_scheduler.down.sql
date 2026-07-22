BEGIN;

DROP TRIGGER IF EXISTS agent_reminder_daemon_owner_event_trigger ON agent;
DROP FUNCTION IF EXISTS project_agent_reminder_daemon_owner_event();
DROP TABLE IF EXISTS agent_reminder_daemon_owner_cursor;
DROP TABLE IF EXISTS agent_reminder_daemon_owner_event;
DROP SEQUENCE IF EXISTS agent_reminder_placement_generation_seq;

ALTER TABLE agent_reminder
  DROP COLUMN IF EXISTS version;

COMMIT;
