BEGIN;

-- Managed patrol cadence is driven by issue progress, never chat quietness.
-- An active task has four controlled next-check choices: 15m, 30m, 45m, or
-- 60m. The group manager chooses one after a patrol wake; the server validates
-- and stores that choice on this single Reminder definition. The server also
-- arms a bounded automatic fallback, resets any real issue progress to 15m,
-- and forces blocked work to remain on the 15m check.
--
-- A group with no active issue becomes dormant: the Reminder definition stays
-- durable in `fired` state, emits no Agent wake, and is re-armed by the next
-- issue progress event.
ALTER TABLE agent_reminder
  ADD COLUMN IF NOT EXISTS managed_backoff_step SMALLINT NOT NULL DEFAULT 0;

ALTER TABLE agent_reminder
  DROP CONSTRAINT IF EXISTS agent_reminder_managed_backoff_step_check;
ALTER TABLE agent_reminder
  ADD CONSTRAINT agent_reminder_managed_backoff_step_check
  CHECK (managed_backoff_step BETWEEN 0 AND 3);

CREATE OR REPLACE FUNCTION group_manager_patrol_channel_has_active_issue(
  target_workspace_id UUID,
  target_channel_id UUID
)
RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1
    FROM channel ch
    JOIN issue work
      ON work.workspace_id = ch.workspace_id
     AND work.status NOT IN ('done', 'cancelled')
     AND (
       (
         ch.project_id IS NOT NULL
         AND work.project_id = ch.project_id
       )
       OR EXISTS (
         SELECT 1
         FROM issue_source_message source
         WHERE source.issue_id = work.id
           AND source.workspace_id = ch.workspace_id
           AND source.channel_id = ch.id
       )
     )
    WHERE ch.id = target_channel_id
      AND ch.workspace_id = target_workspace_id
      AND ch.kind = 'group'
      AND ch.archived_at IS NULL
  );
$$ LANGUAGE SQL STABLE;

CREATE OR REPLACE FUNCTION refresh_group_manager_patrol_for_channel(
  target_workspace_id UUID,
  target_channel_id UUID,
  refresh_reason TEXT
)
RETURNS VOID AS $$
DECLARE
  patrol RECORD;
  has_active_issue BOOLEAN;
  next_fire_at TIMESTAMPTZ;
BEGIN
  has_active_issue := group_manager_patrol_channel_has_active_issue(
    target_workspace_id,
    target_channel_id
  );
  next_fire_at := now() + interval '15 minutes';

  FOR patrol IN
    SELECT reminder.*
    FROM agent_reminder reminder
    WHERE reminder.workspace_id = target_workspace_id
      AND reminder.anchor_channel_id = target_channel_id
      AND reminder.origin_kind = 'group_manager_auto'
      AND reminder.managed_kind = 'patrol'
      AND reminder.status IN ('scheduled', 'fired')
    FOR UPDATE
  LOOP
    IF has_active_issue THEN
      UPDATE agent_reminder
      SET status = 'scheduled',
          fire_at = next_fire_at,
          cadence = NULL,
          schedule_timezone = NULL,
          cadence_next_at = NULL,
          current_occurrence_id = NULL,
          terminal_reason = NULL,
          fired_task_id = NULL,
          managed_backoff_step = 0,
          version = version + 1,
          updated_at = now()
      WHERE id = patrol.id;

      INSERT INTO agent_reminder_lifecycle_event (
        reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
        previous_fire_at, next_fire_at, title_snapshot, cadence_snapshot,
        resulting_state, reason_code, details
      ) VALUES (
        patrol.id, patrol.workspace_id, patrol.agent_id,
        'updated', 'system', patrol.agent_id,
        patrol.fire_at, next_fire_at, patrol.title, NULL,
        'scheduled', refresh_reason,
        jsonb_build_object(
          'policy', 'group_manager_issue_progress_v1',
          'active_issue', true,
          'backoff_step', 0,
          'delay_seconds', 900
        )
      );
    ELSIF patrol.status = 'scheduled' THEN
      UPDATE agent_reminder
      SET status = 'fired',
          cadence = NULL,
          schedule_timezone = NULL,
          cadence_next_at = NULL,
          current_occurrence_id = NULL,
          terminal_reason = NULL,
          managed_backoff_step = 0,
          version = version + 1,
          updated_at = now()
      WHERE id = patrol.id;

      INSERT INTO agent_reminder_lifecycle_event (
        reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
        previous_fire_at, title_snapshot, cadence_snapshot,
        resulting_state, reason_code, details
      ) VALUES (
        patrol.id, patrol.workspace_id, patrol.agent_id,
        'updated', 'system', patrol.agent_id,
        patrol.fire_at, patrol.title, NULL,
        'fired', 'patrol_no_active_issue_dormant',
        jsonb_build_object(
          'policy', 'group_manager_issue_progress_v1',
          'active_issue', false
        )
      );
    END IF;
  END LOOP;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION refresh_group_manager_patrol_for_issue()
RETURNS TRIGGER AS $$
DECLARE
  target_issue_id UUID;
  target_workspace_id UUID;
  target_project_id UUID;
  linked_channel RECORD;
BEGIN
  -- UPDATE OF fires when a column appears in SET, even when its value does
  -- not change. Canonical UpdateIssue writes these columns back on every
  -- update, so only distinct status/assignee/project values are progress.
  IF TG_OP = 'UPDATE'
     AND OLD.status IS NOT DISTINCT FROM NEW.status
     AND OLD.assignee_type IS NOT DISTINCT FROM NEW.assignee_type
     AND OLD.assignee_id IS NOT DISTINCT FROM NEW.assignee_id
     AND OLD.project_id IS NOT DISTINCT FROM NEW.project_id THEN
    RETURN NEW;
  END IF;

  target_issue_id := COALESCE(NEW.id, OLD.id);
  target_workspace_id := COALESCE(NEW.workspace_id, OLD.workspace_id);
  target_project_id := COALESCE(NEW.project_id, OLD.project_id);

  -- A project move changes two patrol scopes. Refresh the previous project
  -- first so a group that no longer owns this issue can become dormant.
  IF TG_OP = 'UPDATE'
     AND OLD.project_id IS DISTINCT FROM NEW.project_id
     AND OLD.project_id IS NOT NULL THEN
    FOR linked_channel IN
      SELECT ch.workspace_id, ch.id
      FROM channel ch
      WHERE ch.workspace_id = OLD.workspace_id
        AND ch.kind = 'group'
        AND ch.archived_at IS NULL
        AND ch.project_id = OLD.project_id
    LOOP
      PERFORM refresh_group_manager_patrol_for_channel(
        linked_channel.workspace_id,
        linked_channel.id,
        'patrol_issue_scope_changed'
      );
    END LOOP;
  END IF;

  FOR linked_channel IN
    SELECT DISTINCT ch.workspace_id, ch.id
    FROM channel ch
    WHERE ch.workspace_id = target_workspace_id
      AND ch.kind = 'group'
      AND ch.archived_at IS NULL
      AND (
        (target_project_id IS NOT NULL AND ch.project_id = target_project_id)
        OR EXISTS (
          SELECT 1
          FROM issue_source_message source
          WHERE source.issue_id = target_issue_id
            AND source.workspace_id = ch.workspace_id
            AND source.channel_id = ch.id
        )
      )
  LOOP
    PERFORM refresh_group_manager_patrol_for_channel(
      linked_channel.workspace_id,
      linked_channel.id,
      'patrol_issue_progress_reset'
    );
  END LOOP;
  RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION refresh_group_manager_patrol_from_issue_child()
RETURNS TRIGGER AS $$
DECLARE
  target_issue_id UUID;
  target_issue issue%ROWTYPE;
BEGIN
  target_issue_id := COALESCE(NEW.issue_id, OLD.issue_id);
  SELECT * INTO target_issue FROM issue WHERE id = target_issue_id;
  IF FOUND THEN
    PERFORM refresh_group_manager_patrol_for_issue_row(target_issue);
  END IF;
  RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

-- PostgreSQL cannot pass a table row through the zero-argument trigger
-- function above, so keep the shared issue->group routing in one typed helper.
CREATE OR REPLACE FUNCTION refresh_group_manager_patrol_for_issue_row(target_issue issue)
RETURNS VOID AS $$
DECLARE
  linked_channel RECORD;
BEGIN
  FOR linked_channel IN
    SELECT DISTINCT ch.workspace_id, ch.id
    FROM channel ch
    WHERE ch.workspace_id = target_issue.workspace_id
      AND ch.kind = 'group'
      AND ch.archived_at IS NULL
      AND (
        (
          target_issue.project_id IS NOT NULL
          AND ch.project_id = target_issue.project_id
        )
        OR EXISTS (
          SELECT 1
          FROM issue_source_message source
          WHERE source.issue_id = target_issue.id
            AND source.workspace_id = ch.workspace_id
            AND source.channel_id = ch.id
        )
      )
  LOOP
    PERFORM refresh_group_manager_patrol_for_channel(
      linked_channel.workspace_id,
      linked_channel.id,
      'patrol_issue_progress_reset'
    );
  END LOOP;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION refresh_group_manager_patrol_from_source()
RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP <> 'INSERT' THEN
    PERFORM refresh_group_manager_patrol_for_channel(
      OLD.workspace_id,
      OLD.channel_id,
      'patrol_issue_scope_changed'
    );
  END IF;
  IF TG_OP <> 'DELETE' THEN
    PERFORM refresh_group_manager_patrol_for_channel(
      NEW.workspace_id,
      NEW.channel_id,
      'patrol_issue_scope_changed'
    );
  END IF;
  RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION refresh_group_manager_patrol_from_channel_project()
RETURNS TRIGGER AS $$
BEGIN
  IF NEW.project_id IS DISTINCT FROM OLD.project_id THEN
    PERFORM refresh_group_manager_patrol_for_channel(
      NEW.workspace_id,
      NEW.id,
      'patrol_issue_scope_changed'
    );
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS issue_group_manager_patrol_progress_trigger ON issue;
CREATE TRIGGER issue_group_manager_patrol_progress_trigger
AFTER INSERT OR UPDATE OF status, assignee_type, assignee_id, project_id
ON issue
FOR EACH ROW EXECUTE FUNCTION refresh_group_manager_patrol_for_issue();

DROP TRIGGER IF EXISTS comment_group_manager_patrol_progress_trigger ON comment;
CREATE TRIGGER comment_group_manager_patrol_progress_trigger
AFTER INSERT OR UPDATE OF content
ON comment
FOR EACH ROW EXECUTE FUNCTION refresh_group_manager_patrol_from_issue_child();

DROP TRIGGER IF EXISTS task_group_manager_patrol_progress_trigger ON agent_task_queue;
CREATE TRIGGER task_group_manager_patrol_progress_trigger
AFTER INSERT OR UPDATE OF status
ON agent_task_queue
FOR EACH ROW
WHEN (NEW.issue_id IS NOT NULL)
EXECUTE FUNCTION refresh_group_manager_patrol_from_issue_child();

DROP TRIGGER IF EXISTS issue_source_group_manager_patrol_scope_trigger ON issue_source_message;
CREATE TRIGGER issue_source_group_manager_patrol_scope_trigger
AFTER INSERT OR UPDATE OR DELETE
ON issue_source_message
FOR EACH ROW EXECUTE FUNCTION refresh_group_manager_patrol_from_source();

DROP TRIGGER IF EXISTS channel_project_group_manager_patrol_scope_trigger ON channel;
CREATE TRIGGER channel_project_group_manager_patrol_scope_trigger
AFTER UPDATE OF project_id
ON channel
FOR EACH ROW EXECUTE FUNCTION refresh_group_manager_patrol_from_channel_project();

-- One-time cutover of existing managed patrols. It is deliberately guarded by
-- a lifecycle marker so reapplying the migration cannot create duplicate audit
-- rows or move an already-migrated timer.
DO $$
DECLARE
  managed_channel RECORD;
BEGIN
  FOR managed_channel IN
    SELECT reminder.workspace_id, reminder.anchor_channel_id
    FROM agent_reminder reminder
    WHERE reminder.origin_kind = 'group_manager_auto'
      AND reminder.managed_kind = 'patrol'
      AND NOT EXISTS (
        SELECT 1
        FROM agent_reminder_lifecycle_event lifecycle
        WHERE lifecycle.reminder_id = reminder.id
          AND lifecycle.reason_code = 'patrol_issue_progress_policy_migrated'
      )
  LOOP
    PERFORM refresh_group_manager_patrol_for_channel(
      managed_channel.workspace_id,
      managed_channel.anchor_channel_id,
      'patrol_issue_progress_policy_migrated'
    );
  END LOOP;
END;
$$;

COMMIT;
