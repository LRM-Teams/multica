-- V6 Work Items own execution. Their one-to-one research_task rows exist for
-- frozen contract identity and provenance, so those rows must mirror the Work
-- Item lifecycle instead of remaining permanently pending.

CREATE FUNCTION research_v6_task_status_for_work_item(work_status TEXT)
RETURNS TEXT
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT CASE work_status
    WHEN 'ready' THEN 'ready'
    WHEN 'dispatching' THEN 'dispatching'
    WHEN 'running' THEN 'running'
    WHEN 'awaiting_input' THEN 'running'
    WHEN 'done' THEN 'succeeded'
    WHEN 'succeeded' THEN 'succeeded'
    WHEN 'failed' THEN 'failed'
    WHEN 'cancelled' THEN 'cancelled'
    WHEN 'stale' THEN 'obsolete'
    ELSE 'pending'
  END
$$;

-- A V6 backing Task may legally follow Work Item recovery transitions that are
-- terminal in the legacy Task state machine (for example cancelled -> ready).
-- Permit only the exact status derived from its owning Work Item; ordinary
-- Research Tasks retain the original transition matrix.
CREATE OR REPLACE FUNCTION enforce_research_task_status_transition()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  expected_status TEXT;
BEGIN
  IF NEW.work_item_id IS NOT NULL THEN
    SELECT research_v6_task_status_for_work_item(work.status)
    INTO expected_status
    FROM research_work_item AS work
    JOIN research_session AS session ON session.id = work.session_id
    WHERE work.id = NEW.work_item_id
      AND work.workspace_id = NEW.workspace_id
      AND work.session_id = NEW.session_id
      AND session.orchestrator_version = 'research-run-v6';

    IF expected_status IS NULL OR NEW.status IS DISTINCT FROM expected_status THEN
      RAISE EXCEPTION 'illegal research_task backing status: %; owning Work Item requires %', NEW.status, expected_status
        USING ERRCODE = '23514', CONSTRAINT = 'research_task_status_transition_check';
    END IF;
    RETURN NEW;
  END IF;

  IF NOT research_task_status_transition_allowed(OLD.status, NEW.status) THEN
    RAISE EXCEPTION 'illegal research_task status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514', CONSTRAINT = 'research_task_status_transition_check';
  END IF;
  RETURN NEW;
END
$$;

CREATE FUNCTION research_v6_backing_task_status_sync_fn()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  UPDATE research_task
  SET status = research_v6_task_status_for_work_item(NEW.status),
      assigned_agent_id = NEW.assigned_agent_id,
      ready_at = NEW.ready_at,
      started_at = NEW.started_at,
      completed_at = CASE
        WHEN NEW.status IN ('done', 'succeeded', 'failed', 'cancelled', 'stale')
          THEN COALESCE(NEW.completed_at, now())
        ELSE NULL
      END,
      terminal_reason = CASE
        WHEN NEW.status IN ('failed', 'cancelled', 'stale')
          THEN COALESCE(NULLIF(NEW.terminal_reason_detail, ''), NULLIF(NEW.terminal_reason_code, ''), '')
        ELSE ''
      END,
      updated_at = now()
  WHERE work_item_id = NEW.id
    AND workspace_id = NEW.workspace_id
    AND session_id = NEW.session_id;
  RETURN NEW;
END
$$;

CREATE TRIGGER research_v6_backing_task_status_sync
AFTER UPDATE OF status, assigned_agent_id, ready_at, started_at, completed_at,
  terminal_reason_code, terminal_reason_detail ON research_work_item
FOR EACH ROW
EXECUTE FUNCTION research_v6_backing_task_status_sync_fn();

-- Existing V6 backing Tasks were created as pending regardless of the Work
-- Item state. Repair them from their owning Work Item.
UPDATE research_task AS task
SET status = research_v6_task_status_for_work_item(work.status),
    assigned_agent_id = work.assigned_agent_id,
    ready_at = work.ready_at,
    started_at = work.started_at,
    completed_at = CASE
      WHEN work.status IN ('done', 'succeeded', 'failed', 'cancelled', 'stale')
        THEN COALESCE(work.completed_at, now())
      ELSE NULL
    END,
    terminal_reason = CASE
      WHEN work.status IN ('failed', 'cancelled', 'stale')
        THEN COALESCE(NULLIF(work.terminal_reason_detail, ''), NULLIF(work.terminal_reason_code, ''), '')
      ELSE ''
    END,
    updated_at = now()
FROM research_work_item AS work
JOIN research_session AS session ON session.id = work.session_id
WHERE task.work_item_id = work.id
  AND task.workspace_id = work.workspace_id
  AND task.session_id = work.session_id
  AND session.orchestrator_version = 'research-run-v6';
