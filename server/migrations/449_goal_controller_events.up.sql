-- Durable event ledger for the standard Goal -> Issue -> Run controller.
-- Work Graph epochs remain an independent optional execution surface.

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;
ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention','dm','ambient','thread_reply','channel_message',
    'chat_session','voice_call','issue_thread_backflow','collaboration_turn',
    'collaboration_manager_fallback','channel_onboarding','issue','quick_create',
    'autopilot','agent_radar','training','environment_dispatch','memory_curation',
    'reminder','channel_role_changed','goal_graph_delta','goal_controller','note_worker'
  ));

CREATE TABLE goal_controller_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  goal_id UUID NOT NULL,
  event_kind TEXT NOT NULL CHECK (length(btrim(event_kind)) BETWEEN 1 AND 80),
  source_kind TEXT NOT NULL CHECK (length(btrim(source_kind)) BETWEEN 1 AND 80),
  source_id UUID,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','dispatched','cancelled')),
  run_id UUID REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, goal_id)
    REFERENCES channel_goal(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX goal_controller_event_pending_idx
  ON goal_controller_event(available_at, created_at, goal_id)
  WHERE status = 'pending';
CREATE INDEX goal_controller_event_goal_idx
  ON goal_controller_event(workspace_id, goal_id);
CREATE INDEX goal_controller_event_run_idx
  ON goal_controller_event(run_id)
  WHERE run_id IS NOT NULL;

-- Existing active Goals need one bootstrap reconciliation after rollout.
INSERT INTO goal_controller_event(
  workspace_id, goal_id, event_kind, source_kind, source_id, payload
)
SELECT workspace_id, id, 'goal_activated', 'channel_goal', id,
       jsonb_build_object('version', version, 'backfill', true)
FROM channel_goal
WHERE status = 'active';

CREATE FUNCTION enqueue_goal_controller_event(
  event_workspace_id UUID,
  event_goal_id UUID,
  event_kind_value TEXT,
  event_source_kind TEXT,
  event_source_id UUID,
  event_payload JSONB DEFAULT '{}'::jsonb
) RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  INSERT INTO goal_controller_event(
    workspace_id, goal_id, event_kind, source_kind, source_id, payload
  )
  SELECT
    event_workspace_id, event_goal_id, event_kind_value, event_source_kind,
    event_source_id, COALESCE(event_payload, '{}'::jsonb)
  FROM channel_goal goal
  WHERE goal.workspace_id=event_workspace_id AND goal.id=event_goal_id;
END;
$$;

CREATE FUNCTION channel_goal_controller_event_trigger()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.status = 'active' THEN
      PERFORM enqueue_goal_controller_event(
        NEW.workspace_id, NEW.id, 'goal_activated', 'channel_goal', NEW.id,
        jsonb_build_object('version', NEW.version)
      );
    END IF;
    RETURN NEW;
  END IF;

  IF NEW.status = 'active' AND (
    OLD.status IS DISTINCT FROM NEW.status
    OR (
      NEW.updated_by_type = 'user'
      AND (
        OLD.version IS DISTINCT FROM NEW.version
        OR OLD.objective IS DISTINCT FROM NEW.objective
        OR OLD.success_criteria IS DISTINCT FROM NEW.success_criteria
      )
    )
  ) THEN
    PERFORM enqueue_goal_controller_event(
      NEW.workspace_id, NEW.id, 'goal_changed', 'channel_goal', NEW.id,
      jsonb_build_object('version', NEW.version)
    );
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER channel_goal_controller_event
AFTER INSERT OR UPDATE ON channel_goal
FOR EACH ROW EXECUTE FUNCTION channel_goal_controller_event_trigger();

CREATE FUNCTION issue_goal_controller_event_trigger()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  scoped_goal_id UUID;
BEGIN
  IF TG_OP = 'DELETE' THEN
    scoped_goal_id := OLD.channel_goal_id;
  ELSIF TG_OP = 'INSERT' THEN
    scoped_goal_id := NEW.channel_goal_id;
  ELSE
    scoped_goal_id := COALESCE(NEW.channel_goal_id, OLD.channel_goal_id);
  END IF;

  IF scoped_goal_id IS NULL THEN
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
  END IF;

  IF TG_OP = 'INSERT' OR TG_OP = 'DELETE' THEN
    PERFORM enqueue_goal_controller_event(
      CASE WHEN TG_OP = 'DELETE' THEN OLD.workspace_id ELSE NEW.workspace_id END,
      scoped_goal_id,
      CASE WHEN TG_OP = 'INSERT' THEN 'issue_created' ELSE 'issue_deleted' END,
      'issue', CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END,
      jsonb_build_object(
        'operation', lower(TG_OP),
        'status', CASE WHEN TG_OP = 'DELETE' THEN OLD.status ELSE NEW.status END
      )
    );
  ELSIF (
    OLD.channel_goal_id IS DISTINCT FROM NEW.channel_goal_id
    OR OLD.status IS DISTINCT FROM NEW.status
    OR OLD.assignee_type IS DISTINCT FROM NEW.assignee_type
    OR OLD.assignee_id IS DISTINCT FROM NEW.assignee_id
    OR OLD.parent_issue_id IS DISTINCT FROM NEW.parent_issue_id
    OR OLD.goal_required IS DISTINCT FROM NEW.goal_required
    OR OLD.acceptance_criteria IS DISTINCT FROM NEW.acceptance_criteria
    OR OLD.execution_revision IS DISTINCT FROM NEW.execution_revision
  ) THEN
    PERFORM enqueue_goal_controller_event(
      NEW.workspace_id, scoped_goal_id, 'issue_changed', 'issue', NEW.id,
      jsonb_build_object(
        'operation', 'update', 'status', NEW.status
      )
    );
  END IF;
  IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;

CREATE TRIGGER issue_goal_controller_event
AFTER INSERT OR UPDATE OR DELETE ON issue
FOR EACH ROW EXECUTE FUNCTION issue_goal_controller_event_trigger();

CREATE FUNCTION issue_dependency_goal_controller_event_trigger()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  dependency_row issue_dependency%ROWTYPE;
  scoped_workspace_id UUID;
  scoped_goal_id UUID;
BEGIN
  IF TG_OP = 'DELETE' THEN dependency_row := OLD; ELSE dependency_row := NEW; END IF;
  SELECT workspace_id, channel_goal_id
    INTO scoped_workspace_id, scoped_goal_id
  FROM issue
  WHERE id = dependency_row.issue_id;

  IF scoped_goal_id IS NOT NULL THEN
    PERFORM enqueue_goal_controller_event(
      scoped_workspace_id, scoped_goal_id, 'dependency_changed',
      'issue_dependency', dependency_row.id,
      jsonb_build_object('operation', lower(TG_OP), 'issue_id', dependency_row.issue_id)
    );
  END IF;
  IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;

CREATE TRIGGER issue_dependency_goal_controller_event
AFTER INSERT OR UPDATE OR DELETE ON issue_dependency
FOR EACH ROW EXECUTE FUNCTION issue_dependency_goal_controller_event_trigger();

CREATE FUNCTION issue_run_goal_controller_event_trigger()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  scoped_workspace_id UUID;
  scoped_goal_id UUID;
BEGIN
  IF NEW.issue_id IS NULL OR NEW.terminal_outcome IS NULL
     OR OLD.terminal_outcome IS NOT DISTINCT FROM NEW.terminal_outcome THEN
    RETURN NEW;
  END IF;

  SELECT workspace_id, channel_goal_id
    INTO scoped_workspace_id, scoped_goal_id
  FROM issue
  WHERE id = NEW.issue_id;

  IF scoped_goal_id IS NOT NULL THEN
    PERFORM enqueue_goal_controller_event(
      scoped_workspace_id, scoped_goal_id, 'run_finished',
      'agent_inbox_event', NEW.id,
      jsonb_build_object(
        'issue_id', NEW.issue_id,
        'terminal_outcome', NEW.terminal_outcome
      )
    );
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER issue_run_goal_controller_event
AFTER UPDATE ON agent_inbox_event
FOR EACH ROW EXECUTE FUNCTION issue_run_goal_controller_event_trigger();
