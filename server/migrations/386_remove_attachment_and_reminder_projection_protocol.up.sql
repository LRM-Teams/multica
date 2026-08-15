BEGIN;

-- agent:start/agent:stop now own local Agent process admission directly.
-- Remove the competing durable Attachment command/replay state machine.
DROP TRIGGER IF EXISTS z_agent_attachment_projection_trigger ON agent;
DROP FUNCTION IF EXISTS project_agent_attachment_projection();
DROP TABLE IF EXISTS agent_attachment_projection_receipt;
DROP TABLE IF EXISTS agent_attachment_projection_cursor;
DROP TABLE IF EXISTS agent_attachment_projection_event;
DROP TABLE IF EXISTS agent_attachment_projection;

-- Reminder reconnect now requests one full snapshot per Runtime, while live
-- mutations publish direct upsert/cancel messages. There is no ordered
-- projection stream, replay cursor, watermark, or placement generation.
DROP TRIGGER IF EXISTS agent_reminder_daemon_timer_event_trigger ON agent_reminder;
DROP FUNCTION IF EXISTS project_agent_reminder_daemon_timer_event();
DROP FUNCTION IF EXISTS enqueue_agent_reminder_daemon_projection(UUID, UUID, UUID, UUID, BIGINT, TEXT, BIGINT, TIMESTAMPTZ, BOOLEAN);

DROP TRIGGER IF EXISTS agent_reminder_daemon_owner_event_trigger ON agent;
DROP FUNCTION IF EXISTS project_agent_reminder_daemon_owner_event();

-- The removed projection trigger also enforced the independent product rule
-- that archiving an Agent terminalizes its live Reminders. Keep that invariant
-- in a narrow trigger with no ownership event, cursor, or generation side
-- effects.
CREATE OR REPLACE FUNCTION cancel_agent_reminders_on_archive()
RETURNS TRIGGER AS $$
DECLARE
  reminder_row agent_reminder%ROWTYPE;
BEGIN
  IF OLD.archived_at IS NULL AND NEW.archived_at IS NOT NULL THEN
    FOR reminder_row IN
      UPDATE agent_reminder
      SET status = 'cancelled', terminal_reason = 'agent_archived',
          current_occurrence_id = NULL, version = version + 1, updated_at = now()
      WHERE agent_id = OLD.id AND status IN ('scheduled', 'firing')
      RETURNING *
    LOOP
      INSERT INTO agent_reminder_lifecycle_event (
        reminder_id, workspace_id, agent_id, event_type, actor_type,
        previous_fire_at, title_snapshot, cadence_snapshot, timezone_snapshot,
        resulting_state, reason_code
      ) VALUES (
        reminder_row.id, reminder_row.workspace_id, reminder_row.agent_id,
        'cancelled', 'system', reminder_row.fire_at, reminder_row.title,
        reminder_row.cadence, reminder_row.schedule_timezone,
        'cancelled', 'agent_archived'
      );
    END LOOP;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cancel_agent_reminders_on_archive_trigger
AFTER UPDATE OF archived_at ON agent
FOR EACH ROW EXECUTE FUNCTION cancel_agent_reminders_on_archive();

DROP TABLE IF EXISTS agent_reminder_daemon_projection_event;
DROP TABLE IF EXISTS agent_reminder_daemon_projection_cursor;
DROP TABLE IF EXISTS agent_reminder_daemon_owner_cursor;
DROP TABLE IF EXISTS agent_reminder_daemon_owner_event;
DROP SEQUENCE IF EXISTS agent_reminder_placement_generation_seq;

COMMIT;
