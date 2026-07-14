-- Bind scheduled Radar to one explicit Wendy supervisor per workspace. Wendy
-- remains a private, user-owned agent; this table records the separate
-- workspace-level responsibility without relying on a mutable display name at
-- scheduling time.
ALTER TABLE agent
  ADD CONSTRAINT uq_agent_workspace_id UNIQUE (workspace_id, id);

-- Completion is a two-phase operation: the daemon first claims the Radar run
-- for action execution, then records the terminal outcome. Keeping executing
-- inside the active-run guard prevents a second task from being scheduled while
-- the first completion request is applying visible actions.
ALTER TABLE agent_radar_run
  DROP CONSTRAINT agent_radar_run_status_check;
ALTER TABLE agent_radar_run
  ADD CONSTRAINT agent_radar_run_status_check
  CHECK (status IN ('planned', 'queued', 'running', 'executing', 'succeeded', 'no_action', 'failed', 'cancelled'));

-- Old application replicas keep their compiled scheduler during a rolling
-- deployment. Reject every active event-driven run and every scheduled run
-- using the legacy per-agent cooldown even when the INSERT comes from an old
-- binary. Manual runs remain available for explicit user requests. NOT VALID
-- lets historical terminal rows remain queryable; the repair below closes
-- every pre-existing unauthorized active row before validation.
ALTER TABLE agent_radar_run
  ADD CONSTRAINT agent_radar_run_active_scheduled_workspace_check
  CHECK (
    status NOT IN ('planned', 'queued', 'running', 'executing')
    OR trigger_kind = 'manual'
    OR (
      trigger_kind = 'scheduled'
      AND cooldown_key = 'workspace_supervisor_radar'
    )
  ) NOT VALID;

DROP INDEX idx_agent_radar_run_active_agent;
CREATE UNIQUE INDEX idx_agent_radar_run_active_agent
  ON agent_radar_run (workspace_id, agent_id)
  WHERE status IN ('planned', 'queued', 'running', 'executing');

CREATE TABLE workspace_radar_state (
  workspace_id UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
  supervisor_agent_id UUID NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  next_due_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_success_at TIMESTAMPTZ,
  last_full_review_at TIMESTAMPTZ,
  last_applied_scheduled_for TIMESTAMPTZ,
  change_version BIGINT NOT NULL DEFAULT 0 CHECK (change_version >= 0),
  change_cursor_version BIGINT NOT NULL DEFAULT 0 CHECK (change_cursor_version >= 0),
  static_scan_cursors JSONB NOT NULL DEFAULT '{}'::jsonb,
  static_cycle_seen JSONB NOT NULL DEFAULT '{}'::jsonb,
  consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT workspace_radar_supervisor_agent_fk
    FOREIGN KEY (workspace_id, supervisor_agent_id)
    REFERENCES agent(workspace_id, id)
    ON DELETE CASCADE
);

-- Workspace changes are assigned a transaction-ordered version by locking the
-- workspace state row. A plain global sequence is not sufficient: a writer can
-- allocate a smaller value and commit after the scheduler has already advanced
-- past a larger sentinel. The row lock makes version order follow commit order
-- for one workspace while leaving unrelated workspaces independent.
CREATE TABLE workspace_radar_change (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  entity_kind TEXT NOT NULL,
  entity_id UUID NOT NULL,
  change_version BIGINT NOT NULL CHECK (change_version > 0),
  occurred_at TIMESTAMPTZ NOT NULL,
  target_kind TEXT NOT NULL,
  target_id UUID,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  PRIMARY KEY (workspace_id, entity_kind, entity_id),
  UNIQUE (workspace_id, change_version)
);

CREATE INDEX idx_workspace_radar_change_pending
  ON workspace_radar_change(workspace_id, change_version);

-- The exact page shown to a scheduled run is persisted with that run. Success
-- may advance only to this receipt; retries and late completions cannot consume
-- changes that were never in the prompt.
CREATE TABLE workspace_radar_run_scan (
  radar_run_id UUID PRIMARY KEY REFERENCES agent_radar_run(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  observed_at TIMESTAMPTZ NOT NULL,
  observed_change_version BIGINT NOT NULL CHECK (observed_change_version >= 0),
  change_cursor_through_version BIGINT NOT NULL CHECK (change_cursor_through_version >= 0),
  changes_has_more BOOLEAN NOT NULL DEFAULT FALSE,
  static_next_cursors JSONB NOT NULL DEFAULT '{}'::jsonb,
  static_wrapped_sections JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_workspace_radar_run_scan_workspace
  ON workspace_radar_run_scan(workspace_id, created_at DESC);

-- Time-based conditions have no source-table UPDATE at the moment they become
-- actionable. Remember each emitted threshold so overdue/stale rows enter the
-- same durable change backlog once, then become eligible again only after the
-- underlying threshold changes.
CREATE TABLE workspace_radar_time_signal (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  signal_kind TEXT NOT NULL,
  entity_id UUID NOT NULL,
  threshold_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, signal_kind, entity_id)
);

-- These rows identify artifacts created by a scheduled Wendy directive while
-- its action receipt is executing. Initial comment/message/queue writes stay
-- visible to users but do not schedule another model call. Replies, progress,
-- failures and terminal outcomes are still journalled normally.
CREATE TABLE workspace_radar_directive_artifact (
  artifact_kind TEXT NOT NULL,
  artifact_id UUID NOT NULL,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  radar_action_id UUID NOT NULL REFERENCES agent_radar_action(id) ON DELETE CASCADE,
  target_agent_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (artifact_kind, artifact_id)
);

CREATE INDEX idx_workspace_radar_directive_artifact_workspace
  ON workspace_radar_directive_artifact(workspace_id, created_at DESC);

CREATE FUNCTION record_workspace_radar_change(
  changed_workspace_id UUID,
  changed_entity_kind TEXT,
  changed_entity_id UUID,
  changed_occurred_at TIMESTAMPTZ,
  changed_target_kind TEXT,
  changed_target_id UUID,
  changed_payload JSONB
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  assigned_version BIGINT;
BEGIN
  UPDATE workspace_radar_state
  SET change_version = change_version + 1,
      next_due_at = LEAST(next_due_at, now()),
      updated_at = now()
  WHERE workspace_id = changed_workspace_id
    AND enabled
  RETURNING change_version INTO assigned_version;

  IF assigned_version IS NULL THEN
    RETURN;
  END IF;

  INSERT INTO workspace_radar_change (
    workspace_id, entity_kind, entity_id, change_version, occurred_at,
    target_kind, target_id, payload
  ) VALUES (
    changed_workspace_id, changed_entity_kind, changed_entity_id,
    assigned_version, COALESCE(changed_occurred_at, clock_timestamp()),
    changed_target_kind, changed_target_id, COALESCE(changed_payload, '{}'::jsonb)
  )
  ON CONFLICT (workspace_id, entity_kind, entity_id) DO UPDATE
  SET change_version = EXCLUDED.change_version,
      occurred_at = EXCLUDED.occurred_at,
      target_kind = EXCLUDED.target_kind,
      target_id = EXCLUDED.target_id,
      payload = EXCLUDED.payload;
END;
$$;

CREATE FUNCTION journal_workspace_radar_issue()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value issue%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'issue', row_value.id, clock_timestamp(),
    'issue', row_value.id,
    jsonb_build_object(
      'issue_id', row_value.id,
      'number', row_value.number,
      'title', left(row_value.title, 160),
      'status', CASE WHEN TG_OP = 'DELETE' THEN 'deleted' ELSE row_value.status END,
      'priority', row_value.priority,
      'assignee_type', row_value.assignee_type,
      'assignee_id', row_value.assignee_id,
      'due_date', row_value.due_date,
      'updated_at', row_value.updated_at
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_issue
AFTER INSERT OR UPDATE OR DELETE ON issue
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_issue();

CREATE FUNCTION journal_workspace_radar_comment()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value comment%ROWTYPE;
  directive_action_id UUID;
  directive_target_agent_id UUID;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;

  IF TG_OP = 'INSERT' AND row_value.author_type = 'agent' THEN
    SELECT action.id,
           CASE
             WHEN action.payload->>'target_agent_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
             THEN (action.payload->>'target_agent_id')::uuid
           END
    INTO directive_action_id, directive_target_agent_id
    FROM agent_radar_action action
    JOIN agent_radar_run run ON run.id = action.radar_run_id
    WHERE action.workspace_id = row_value.workspace_id
      AND action.agent_id = row_value.author_id
      AND action.action_type = 'comment_issue'
      AND action.status = 'executing'
      AND action.target_id = row_value.issue_id
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'executing'
    ORDER BY action.created_at DESC, action.id DESC
    LIMIT 1;
    IF directive_action_id IS NOT NULL THEN
      INSERT INTO workspace_radar_directive_artifact (
        artifact_kind, artifact_id, workspace_id, radar_action_id, target_agent_id
      ) VALUES (
        'comment', row_value.id, row_value.workspace_id,
        directive_action_id, directive_target_agent_id
      ) ON CONFLICT DO NOTHING;
      RETURN NEW;
    END IF;
  END IF;

  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'issue_comment', row_value.id, clock_timestamp(),
    'issue', row_value.issue_id,
    jsonb_build_object(
      'comment_id', row_value.id,
      'issue_id', row_value.issue_id,
      'author_type', row_value.author_type,
      'author_id', row_value.author_id,
      'type', row_value.type,
      'content', CASE WHEN TG_OP = 'DELETE' THEN '[deleted]' ELSE left(row_value.content, 500) END,
      'created_at', row_value.created_at,
      'updated_at', row_value.updated_at
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_comment
AFTER INSERT OR UPDATE OR DELETE ON comment
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_comment();

CREATE FUNCTION journal_workspace_radar_agent()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  IF EXISTS (
    SELECT 1 FROM workspace_radar_state state
    WHERE state.workspace_id = row_value.workspace_id
      AND state.supervisor_agent_id = row_value.id
  ) THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  IF TG_OP = 'UPDATE'
     AND OLD.name IS NOT DISTINCT FROM NEW.name
     AND OLD.display_name IS NOT DISTINCT FROM NEW.display_name
     AND OLD.description IS NOT DISTINCT FROM NEW.description
     AND OLD.status IS NOT DISTINCT FROM NEW.status
     AND OLD.runtime_id IS NOT DISTINCT FROM NEW.runtime_id
     AND OLD.archived_at IS NOT DISTINCT FROM NEW.archived_at
     AND OLD.max_concurrent_tasks IS NOT DISTINCT FROM NEW.max_concurrent_tasks THEN
    RETURN NEW;
  END IF;
  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'agent', row_value.id, clock_timestamp(),
    'agent', row_value.id,
    jsonb_build_object(
      'agent_id', row_value.id,
      'name', COALESCE(NULLIF(row_value.display_name, ''), row_value.name),
      'status', CASE WHEN TG_OP = 'DELETE' THEN 'deleted' ELSE row_value.status END,
      'runtime_id', row_value.runtime_id,
      'archived_at', row_value.archived_at,
      'capabilities', left(COALESCE(row_value.description, ''), 300)
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_agent
AFTER INSERT OR UPDATE OR DELETE ON agent
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_agent();

CREATE FUNCTION journal_workspace_radar_runtime()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  linked_agent RECORD;
BEGIN
  -- last_seen_at/updated_at-only writes are daemon heartbeats and must never
  -- create a proactive model call.
  IF TG_OP = 'UPDATE'
     AND OLD.name IS NOT DISTINCT FROM NEW.name
     AND OLD.runtime_mode IS NOT DISTINCT FROM NEW.runtime_mode
     AND OLD.provider IS NOT DISTINCT FROM NEW.provider
     AND OLD.status IS NOT DISTINCT FROM NEW.status
     AND OLD.device_info IS NOT DISTINCT FROM NEW.device_info
     AND OLD.metadata IS NOT DISTINCT FROM NEW.metadata
     AND OLD.visibility IS NOT DISTINCT FROM NEW.visibility
     AND OLD.owner_id IS NOT DISTINCT FROM NEW.owner_id THEN
    RETURN NEW;
  END IF;
  FOR linked_agent IN
    SELECT a.id, a.workspace_id
    FROM agent a
    JOIN workspace_radar_state state
      ON state.workspace_id = a.workspace_id
     AND state.supervisor_agent_id <> a.id
    WHERE a.runtime_id = CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END
  LOOP
    PERFORM record_workspace_radar_change(
      linked_agent.workspace_id, 'agent_runtime', linked_agent.id,
      clock_timestamp(), 'agent', linked_agent.id,
      jsonb_build_object(
        'agent_id', linked_agent.id,
        'runtime_id', CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END,
        'status', CASE WHEN TG_OP = 'DELETE' THEN 'deleted' ELSE NEW.status END,
        'provider', CASE WHEN TG_OP = 'DELETE' THEN OLD.provider ELSE NEW.provider END,
        'runtime_mode', CASE WHEN TG_OP = 'DELETE' THEN OLD.runtime_mode ELSE NEW.runtime_mode END,
        'runtime_name', CASE WHEN TG_OP = 'DELETE' THEN OLD.name ELSE NEW.name END
      )
    );
  END LOOP;
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_runtime
AFTER INSERT OR UPDATE OR DELETE ON agent_runtime
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_runtime();

CREATE FUNCTION journal_workspace_radar_task()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent_task_queue%ROWTYPE;
  task_workspace_id UUID;
  directive workspace_radar_directive_artifact%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  IF row_value.chat_session_id IS NOT NULL
     OR COALESCE(row_value.context->>'type', '') = 'agent_radar' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  SELECT workspace_id INTO task_workspace_id FROM agent WHERE id = row_value.agent_id;
  IF task_workspace_id IS NULL THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;

  IF row_value.trigger_comment_id IS NOT NULL THEN
    SELECT artifact.* INTO directive
    FROM workspace_radar_directive_artifact artifact
    WHERE artifact.artifact_kind = 'comment'
      AND artifact.artifact_id = row_value.trigger_comment_id;
    IF directive.artifact_id IS NOT NULL THEN
      INSERT INTO workspace_radar_directive_artifact (
        artifact_kind, artifact_id, workspace_id, radar_action_id, target_agent_id
      ) VALUES (
        'task', row_value.id, task_workspace_id,
        directive.radar_action_id, row_value.agent_id
      ) ON CONFLICT DO NOTHING;
    END IF;
  END IF;
  IF directive.artifact_id IS NULL THEN
    SELECT artifact.* INTO directive
    FROM workspace_radar_directive_artifact artifact
    WHERE artifact.artifact_kind = 'task'
      AND artifact.artifact_id = row_value.id;
  END IF;

  IF directive.artifact_id IS NOT NULL
     AND TG_OP <> 'DELETE'
     AND row_value.status IN ('queued', 'dispatched', 'running') THEN
    RETURN NEW;
  END IF;

  PERFORM record_workspace_radar_change(
    task_workspace_id, 'task', row_value.id, clock_timestamp(),
    CASE WHEN row_value.issue_id IS NULL THEN 'agent' ELSE 'issue' END,
    COALESCE(row_value.issue_id, row_value.agent_id),
    jsonb_build_object(
      'task_id', row_value.id,
      'agent_id', row_value.agent_id,
      'issue_id', row_value.issue_id,
      'status', CASE WHEN TG_OP = 'DELETE' THEN 'deleted' ELSE row_value.status END,
      'created_at', row_value.created_at,
      'dispatched_at', row_value.dispatched_at,
      'started_at', row_value.started_at,
      'completed_at', row_value.completed_at,
      'wait_reason', left(COALESCE(row_value.wait_reason, ''), 200),
      'failure_reason', left(COALESCE(row_value.failure_reason, ''), 200),
      'error', left(COALESCE(row_value.error, ''), 300),
      'result', left(COALESCE(row_value.result::text, ''), 500),
      'trigger_summary', left(COALESCE(row_value.trigger_summary, ''), 300)
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_task
AFTER INSERT OR UPDATE OR DELETE ON agent_task_queue
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_task();

-- Progress events were previously websocket-only, so a supervisor checking
-- between events could not tell whether a running task was advancing.
CREATE TABLE agent_task_progress_snapshot (
  task_id UUID PRIMARY KEY REFERENCES agent_task_queue(id) ON DELETE CASCADE,
  summary TEXT NOT NULL DEFAULT '',
  step INTEGER NOT NULL DEFAULT 0,
  total INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION journal_workspace_radar_task_progress()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent_task_progress_snapshot%ROWTYPE;
  task_row agent_task_queue%ROWTYPE;
  task_workspace_id UUID;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  SELECT task.* INTO task_row FROM agent_task_queue task WHERE task.id = row_value.task_id;
  IF task_row.id IS NULL
     OR task_row.chat_session_id IS NOT NULL
     OR COALESCE(task_row.context->>'type', '') = 'agent_radar' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  SELECT workspace_id INTO task_workspace_id FROM agent WHERE id = task_row.agent_id;
  PERFORM record_workspace_radar_change(
    task_workspace_id, 'task_progress', row_value.task_id, clock_timestamp(),
    CASE WHEN task_row.issue_id IS NULL THEN 'agent' ELSE 'issue' END,
    COALESCE(task_row.issue_id, task_row.agent_id),
    jsonb_build_object(
      'task_id', row_value.task_id,
      'agent_id', task_row.agent_id,
      'issue_id', task_row.issue_id,
      'task_status', task_row.status,
      'summary', CASE WHEN TG_OP = 'DELETE' THEN '[deleted]' ELSE left(row_value.summary, 500) END,
      'step', row_value.step,
      'total', row_value.total,
      'updated_at', row_value.updated_at
    )
  );
  DELETE FROM workspace_radar_time_signal
  WHERE workspace_id = task_workspace_id
    AND signal_kind = 'stale_task'
    AND entity_id = row_value.task_id;
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_task_progress
AFTER INSERT OR UPDATE OR DELETE ON agent_task_progress_snapshot
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_task_progress();

CREATE FUNCTION journal_workspace_radar_channel()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value channel%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  IF row_value.kind <> 'group' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  IF TG_OP = 'UPDATE' AND EXISTS (
    SELECT 1
    FROM agent_radar_action action
    JOIN agent_radar_run run ON run.id = action.radar_run_id
    WHERE action.workspace_id = row_value.workspace_id
      AND action.target_id = row_value.id
      AND action.action_type = 'mention_agent'
      AND action.status = 'executing'
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'executing'
  ) THEN
    RETURN NEW;
  END IF;
  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'group_channel', row_value.id, clock_timestamp(),
    'channel', row_value.id,
    jsonb_build_object(
      'channel_id', row_value.id,
      'name', left(row_value.name, 160),
      'description', left(COALESCE(row_value.description, ''), 300),
      'archived_at', row_value.archived_at,
      'updated_at', row_value.updated_at,
      'operation', lower(TG_OP)
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_channel
AFTER INSERT OR UPDATE OR DELETE ON channel
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_channel();

CREATE FUNCTION journal_workspace_radar_channel_message()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value channel_message%ROWTYPE;
  channel_row channel%ROWTYPE;
  directive_action_id UUID;
  directive_target_agent_id UUID;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  SELECT * INTO channel_row FROM channel WHERE id = row_value.channel_id;
  IF channel_row.id IS NULL OR channel_row.kind <> 'group' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;

  IF TG_OP = 'INSERT' AND row_value.author_type = 'agent' THEN
    SELECT action.id,
           CASE
             WHEN action.payload->>'target_agent_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
             THEN (action.payload->>'target_agent_id')::uuid
           END
    INTO directive_action_id, directive_target_agent_id
    FROM agent_radar_action action
    JOIN agent_radar_run run ON run.id = action.radar_run_id
    WHERE action.workspace_id = row_value.workspace_id
      AND action.agent_id = row_value.author_id
      AND action.action_type = 'mention_agent'
      AND action.status = 'executing'
      AND action.target_id = row_value.channel_id
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'executing'
    ORDER BY action.created_at DESC, action.id DESC
    LIMIT 1;
    IF directive_action_id IS NOT NULL THEN
      INSERT INTO workspace_radar_directive_artifact (
        artifact_kind, artifact_id, workspace_id, radar_action_id, target_agent_id
      ) VALUES (
        'channel_message', row_value.id, row_value.workspace_id,
        directive_action_id, directive_target_agent_id
      ) ON CONFLICT DO NOTHING;
      RETURN NEW;
    END IF;
  END IF;

  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'group_message', row_value.id, clock_timestamp(),
    'channel', row_value.channel_id,
    jsonb_build_object(
      'channel_id', row_value.channel_id,
      'channel_name', channel_row.name,
      'message_id', row_value.id,
      'seq', row_value.seq,
      'author_type', row_value.author_type,
      'author_id', row_value.author_id,
      'author_name', left(row_value.author_name, 100),
      'content', CASE WHEN TG_OP = 'DELETE' OR row_value.deleted_at IS NOT NULL THEN '[deleted]' ELSE left(row_value.content, 600) END,
      'created_at', row_value.created_at,
      'edited_at', row_value.edited_at,
      'deleted_at', row_value.deleted_at
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_channel_message
AFTER INSERT OR UPDATE OR DELETE ON channel_message
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_channel_message();

CREATE FUNCTION journal_workspace_radar_reminder()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent_reminder%ROWTYPE;
  channel_row channel%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  SELECT * INTO channel_row FROM channel WHERE id = row_value.anchor_channel_id;
  IF channel_row.id IS NULL OR channel_row.kind <> 'group' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'group_reminder', row_value.id, clock_timestamp(),
    'channel', row_value.anchor_channel_id,
    jsonb_build_object(
      'reminder_id', row_value.id,
      'agent_id', row_value.agent_id,
      'channel_id', row_value.anchor_channel_id,
      'channel_name', channel_row.name,
      'title', CASE WHEN TG_OP = 'DELETE' THEN '[deleted]' ELSE left(row_value.title, 500) END,
      'fire_at', row_value.fire_at,
      'status', CASE WHEN TG_OP = 'DELETE' THEN 'deleted' ELSE row_value.status END,
      'updated_at', row_value.updated_at
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_reminder
AFTER INSERT OR UPDATE OR DELETE ON agent_reminder
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_reminder();

CREATE FUNCTION journal_workspace_radar_inbox_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent_inbox_event%ROWTYPE;
  channel_row channel%ROWTYPE;
  directive workspace_radar_directive_artifact%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  SELECT * INTO channel_row FROM channel WHERE id = row_value.channel_id;
  IF channel_row.id IS NULL OR channel_row.kind <> 'group' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  IF row_value.source_message_id IS NOT NULL THEN
    SELECT artifact.* INTO directive
    FROM workspace_radar_directive_artifact artifact
    WHERE artifact.artifact_kind = 'channel_message'
      AND artifact.artifact_id = row_value.source_message_id;
    IF directive.artifact_id IS NOT NULL THEN
      INSERT INTO workspace_radar_directive_artifact (
        artifact_kind, artifact_id, workspace_id, radar_action_id, target_agent_id
      ) VALUES (
        'inbox_event', row_value.id, row_value.workspace_id,
        directive.radar_action_id, row_value.agent_id
      ) ON CONFLICT DO NOTHING;
    END IF;
  END IF;
  IF directive.artifact_id IS NULL THEN
    SELECT artifact.* INTO directive
    FROM workspace_radar_directive_artifact artifact
    WHERE artifact.artifact_kind = 'inbox_event'
      AND artifact.artifact_id = row_value.id;
  END IF;
  IF directive.artifact_id IS NOT NULL
     AND TG_OP <> 'DELETE'
     AND row_value.terminal_outcome IS NULL
     AND row_value.status IN ('pending', 'draining') THEN
    RETURN NEW;
  END IF;
  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'group_inbox_event', row_value.id, clock_timestamp(),
    'channel', row_value.channel_id,
    jsonb_build_object(
      'inbox_event_id', row_value.id,
      'channel_id', row_value.channel_id,
      'channel_name', channel_row.name,
      'agent_id', row_value.agent_id,
      'source_message_id', row_value.source_message_id,
      'reason', row_value.reason,
      'requires_wake', row_value.requires_wake,
      'status', CASE WHEN TG_OP = 'DELETE' THEN 'deleted' ELSE row_value.status END,
      'attempt', row_value.attempt,
      'claimed_at', row_value.claimed_at,
      'terminal_outcome', row_value.terminal_outcome,
      'terminal_at', row_value.terminal_at,
      'retryable', row_value.retryable,
      'last_error', left(COALESCE(row_value.last_error, ''), 400),
      'created_at', row_value.created_at,
      'updated_at', row_value.updated_at
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_inbox_event
AFTER INSERT OR UPDATE OR DELETE ON agent_inbox_event
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_inbox_event();

CREATE FUNCTION journal_workspace_radar_event_delivery()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent_event_delivery%ROWTYPE;
  inbox_row agent_inbox_event%ROWTYPE;
  channel_row channel%ROWTYPE;
  directive workspace_radar_directive_artifact%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  SELECT * INTO inbox_row FROM agent_inbox_event WHERE id = row_value.inbox_event_id;
  SELECT * INTO channel_row FROM channel WHERE id = inbox_row.channel_id;
  IF inbox_row.id IS NULL OR channel_row.id IS NULL OR channel_row.kind <> 'group' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  SELECT artifact.* INTO directive
  FROM workspace_radar_directive_artifact artifact
  WHERE artifact.artifact_kind = 'inbox_event'
    AND artifact.artifact_id = inbox_row.id;
  IF directive.artifact_id IS NOT NULL
     AND TG_OP <> 'DELETE'
     AND row_value.status IN ('leased', 'processing') THEN
    RETURN NEW;
  END IF;
  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'group_inbox_delivery', row_value.id, clock_timestamp(),
    'channel', inbox_row.channel_id,
    jsonb_build_object(
      'delivery_id', row_value.id,
      'inbox_event_id', inbox_row.id,
      'channel_id', inbox_row.channel_id,
      'channel_name', channel_row.name,
      'agent_id', inbox_row.agent_id,
      'status', CASE WHEN TG_OP = 'DELETE' THEN 'deleted' ELSE row_value.status END,
      'leased_at', row_value.leased_at,
      'lease_expires_at', row_value.lease_expires_at,
      'acked_at', row_value.acked_at,
      'last_error', left(COALESCE(row_value.last_error, ''), 400),
      'updated_at', row_value.updated_at
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_journal_workspace_radar_event_delivery
AFTER INSERT OR UPDATE OR DELETE ON agent_event_delivery
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_event_delivery();

CREATE FUNCTION refresh_workspace_radar_time_signals(
  target_workspace_id UUID,
  observed_at TIMESTAMPTZ
)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  candidate RECORD;
  emitted BIGINT := 0;
BEGIN
  DELETE FROM workspace_radar_time_signal signal
  WHERE signal.workspace_id = target_workspace_id
    AND (
      (signal.signal_kind = 'due_issue' AND NOT EXISTS (
        SELECT 1 FROM issue item
        WHERE item.id = signal.entity_id
          AND item.workspace_id = target_workspace_id
          AND item.status NOT IN ('done', 'cancelled')
          AND item.due_date IS NOT NULL
          AND (item.due_date::timestamp AT TIME ZONE 'UTC') <= observed_at
          AND (item.due_date::timestamp AT TIME ZONE 'UTC') = signal.threshold_at
      ))
      OR
      (signal.signal_kind = 'due_group_reminder' AND NOT EXISTS (
        SELECT 1
        FROM agent_reminder reminder
        JOIN channel ch
          ON ch.id = reminder.anchor_channel_id
         AND ch.workspace_id = reminder.workspace_id
         AND ch.kind = 'group'
        WHERE reminder.id = signal.entity_id
          AND reminder.workspace_id = target_workspace_id
          AND reminder.status IN ('scheduled', 'firing')
          AND reminder.fire_at <= observed_at
          AND reminder.fire_at = signal.threshold_at
      ))
      OR
      (signal.signal_kind = 'stale_task' AND NOT EXISTS (
        SELECT 1
        FROM agent_task_queue task
        JOIN agent task_agent ON task_agent.id = task.agent_id
        LEFT JOIN agent_task_progress_snapshot progress ON progress.task_id = task.id
        WHERE task.id = signal.entity_id
          AND task_agent.workspace_id = target_workspace_id
          AND task.chat_session_id IS NULL
          AND COALESCE(task.context->>'type', '') <> 'agent_radar'
          AND task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
          AND GREATEST(
            task.created_at,
            COALESCE(task.started_at, task.created_at),
            COALESCE(progress.updated_at, task.created_at)
          ) + interval '60 minutes' <= observed_at
          AND GREATEST(
            task.created_at,
            COALESCE(task.started_at, task.created_at),
            COALESCE(progress.updated_at, task.created_at)
          ) + interval '60 minutes' = signal.threshold_at
      ))
      OR
      (signal.signal_kind = 'stale_group_inbox' AND NOT EXISTS (
        SELECT 1
        FROM agent_inbox_event inbox
        JOIN channel ch
          ON ch.id = inbox.channel_id
         AND ch.workspace_id = inbox.workspace_id
         AND ch.kind = 'group'
        WHERE inbox.id = signal.entity_id
          AND inbox.workspace_id = target_workspace_id
          AND inbox.status IN ('pending', 'draining')
          AND inbox.terminal_outcome IS NULL
          AND GREATEST(inbox.created_at, inbox.updated_at) + interval '60 minutes' <= observed_at
          AND GREATEST(inbox.created_at, inbox.updated_at) + interval '60 minutes' = signal.threshold_at
      ))
    );

  FOR candidate IN
    SELECT
      item.id,
      (item.due_date::timestamp AT TIME ZONE 'UTC') AS threshold_at,
      jsonb_build_object(
        'signal', 'due_issue',
        'issue_id', item.id,
        'number', item.number,
        'title', left(item.title, 160),
        'status', item.status,
        'priority', item.priority,
        'assignee_type', item.assignee_type,
        'assignee_id', item.assignee_id,
        'due_date', item.due_date
      ) AS payload
    FROM issue item
    LEFT JOIN workspace_radar_time_signal signal
      ON signal.workspace_id = item.workspace_id
     AND signal.signal_kind = 'due_issue'
     AND signal.entity_id = item.id
     AND signal.threshold_at = (item.due_date::timestamp AT TIME ZONE 'UTC')
    WHERE item.workspace_id = target_workspace_id
      AND item.status NOT IN ('done', 'cancelled')
      AND item.due_date IS NOT NULL
      AND (item.due_date::timestamp AT TIME ZONE 'UTC') <= observed_at
      AND signal.entity_id IS NULL
    ORDER BY item.due_date, item.id
  LOOP
    INSERT INTO workspace_radar_time_signal (workspace_id, signal_kind, entity_id, threshold_at)
    VALUES (target_workspace_id, 'due_issue', candidate.id, candidate.threshold_at)
    ON CONFLICT (workspace_id, signal_kind, entity_id) DO UPDATE
    SET threshold_at = EXCLUDED.threshold_at;
    PERFORM record_workspace_radar_change(
      target_workspace_id, 'time_due_issue', candidate.id, candidate.threshold_at,
      'issue', candidate.id, candidate.payload
    );
    emitted := emitted + 1;
  END LOOP;

  FOR candidate IN
    SELECT
      reminder.id,
      reminder.anchor_channel_id,
      reminder.fire_at AS threshold_at,
      jsonb_build_object(
        'signal', 'due_group_reminder',
        'reminder_id', reminder.id,
        'agent_id', reminder.agent_id,
        'channel_id', reminder.anchor_channel_id,
        'channel_name', ch.name,
        'title', left(reminder.title, 500),
        'fire_at', reminder.fire_at,
        'status', reminder.status
      ) AS payload
    FROM agent_reminder reminder
    JOIN channel ch
      ON ch.id = reminder.anchor_channel_id
     AND ch.workspace_id = reminder.workspace_id
     AND ch.kind = 'group'
    LEFT JOIN workspace_radar_time_signal signal
      ON signal.workspace_id = reminder.workspace_id
     AND signal.signal_kind = 'due_group_reminder'
     AND signal.entity_id = reminder.id
     AND signal.threshold_at = reminder.fire_at
    WHERE reminder.workspace_id = target_workspace_id
      AND reminder.status IN ('scheduled', 'firing')
      AND reminder.fire_at <= observed_at
      AND signal.entity_id IS NULL
    ORDER BY reminder.fire_at, reminder.id
  LOOP
    INSERT INTO workspace_radar_time_signal (workspace_id, signal_kind, entity_id, threshold_at)
    VALUES (target_workspace_id, 'due_group_reminder', candidate.id, candidate.threshold_at)
    ON CONFLICT (workspace_id, signal_kind, entity_id) DO UPDATE
    SET threshold_at = EXCLUDED.threshold_at;
    PERFORM record_workspace_radar_change(
      target_workspace_id, 'time_due_group_reminder', candidate.id, candidate.threshold_at,
      'channel', candidate.anchor_channel_id, candidate.payload
    );
    emitted := emitted + 1;
  END LOOP;

  FOR candidate IN
    SELECT
      task.id,
      task.agent_id,
      task.issue_id,
      GREATEST(
        task.created_at,
        COALESCE(task.started_at, task.created_at),
        COALESCE(progress.updated_at, task.created_at)
      ) + interval '60 minutes' AS threshold_at,
      jsonb_build_object(
        'signal', 'stale_task',
        'task_id', task.id,
        'agent_id', task.agent_id,
        'issue_id', task.issue_id,
        'status', task.status,
        'created_at', task.created_at,
        'started_at', task.started_at,
        'progress_at', progress.updated_at,
        'progress_summary', left(COALESCE(progress.summary, ''), 500),
        'stale_since', GREATEST(
          task.created_at,
          COALESCE(task.started_at, task.created_at),
          COALESCE(progress.updated_at, task.created_at)
        ) + interval '60 minutes'
      ) AS payload
    FROM agent_task_queue task
    JOIN agent task_agent ON task_agent.id = task.agent_id
    LEFT JOIN agent_task_progress_snapshot progress ON progress.task_id = task.id
    LEFT JOIN workspace_radar_time_signal signal
      ON signal.workspace_id = task_agent.workspace_id
     AND signal.signal_kind = 'stale_task'
     AND signal.entity_id = task.id
     AND signal.threshold_at = GREATEST(
       task.created_at,
       COALESCE(task.started_at, task.created_at),
       COALESCE(progress.updated_at, task.created_at)
     ) + interval '60 minutes'
    WHERE task_agent.workspace_id = target_workspace_id
      AND task.chat_session_id IS NULL
      AND COALESCE(task.context->>'type', '') <> 'agent_radar'
      AND task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
      AND GREATEST(
        task.created_at,
        COALESCE(task.started_at, task.created_at),
        COALESCE(progress.updated_at, task.created_at)
      ) + interval '60 minutes' <= observed_at
      AND signal.entity_id IS NULL
    ORDER BY threshold_at, task.id
  LOOP
    INSERT INTO workspace_radar_time_signal (workspace_id, signal_kind, entity_id, threshold_at)
    VALUES (target_workspace_id, 'stale_task', candidate.id, candidate.threshold_at)
    ON CONFLICT (workspace_id, signal_kind, entity_id) DO UPDATE
    SET threshold_at = EXCLUDED.threshold_at;
    PERFORM record_workspace_radar_change(
      target_workspace_id, 'time_stale_task', candidate.id, candidate.threshold_at,
      CASE WHEN candidate.issue_id IS NULL THEN 'agent' ELSE 'issue' END,
      COALESCE(candidate.issue_id, candidate.agent_id), candidate.payload
    );
    emitted := emitted + 1;
  END LOOP;

  FOR candidate IN
    SELECT
      inbox.id,
      inbox.agent_id,
      inbox.channel_id,
      GREATEST(inbox.created_at, inbox.updated_at) + interval '60 minutes' AS threshold_at,
      jsonb_build_object(
        'signal', 'stale_group_inbox',
        'inbox_event_id', inbox.id,
        'channel_id', inbox.channel_id,
        'channel_name', ch.name,
        'agent_id', inbox.agent_id,
        'source_message_id', inbox.source_message_id,
        'status', inbox.status,
        'requires_wake', inbox.requires_wake,
        'claimed_at', inbox.claimed_at,
        'last_error', left(COALESCE(inbox.last_error, ''), 400),
        'stale_since', GREATEST(inbox.created_at, inbox.updated_at) + interval '60 minutes'
      ) AS payload
    FROM agent_inbox_event inbox
    JOIN channel ch
      ON ch.id = inbox.channel_id
     AND ch.workspace_id = inbox.workspace_id
     AND ch.kind = 'group'
    LEFT JOIN workspace_radar_time_signal signal
      ON signal.workspace_id = inbox.workspace_id
     AND signal.signal_kind = 'stale_group_inbox'
     AND signal.entity_id = inbox.id
     AND signal.threshold_at = GREATEST(inbox.created_at, inbox.updated_at) + interval '60 minutes'
    WHERE inbox.workspace_id = target_workspace_id
      AND inbox.status IN ('pending', 'draining')
      AND inbox.terminal_outcome IS NULL
      AND GREATEST(inbox.created_at, inbox.updated_at) + interval '60 minutes' <= observed_at
      AND signal.entity_id IS NULL
    ORDER BY threshold_at, inbox.id
  LOOP
    INSERT INTO workspace_radar_time_signal (workspace_id, signal_kind, entity_id, threshold_at)
    VALUES (target_workspace_id, 'stale_group_inbox', candidate.id, candidate.threshold_at)
    ON CONFLICT (workspace_id, signal_kind, entity_id) DO UPDATE
    SET threshold_at = EXCLUDED.threshold_at;
    PERFORM record_workspace_radar_change(
      target_workspace_id, 'time_stale_group_inbox', candidate.id, candidate.threshold_at,
      'channel', candidate.channel_id, candidate.payload
    );
    emitted := emitted + 1;
  END LOOP;

  RETURN emitted;
END;
$$;

CREATE INDEX idx_workspace_radar_state_due
  ON workspace_radar_state(next_due_at, workspace_id)
  WHERE enabled;

-- Every task authorization check resolves the linked run from the queue task.
-- Without this index, each daemon candidate/claim/start check scans the full
-- Radar history as it grows.
CREATE INDEX idx_agent_radar_run_task_id
  ON agent_radar_run(task_id)
  WHERE task_id IS NOT NULL;

-- Authorization must also be enforced by PostgreSQL because an old server
-- replica retains the pre-migration ClaimAgentTask/Reclaim/Start SQL.  Returning
-- NULL from a BEFORE trigger suppresses the UPDATE, so UPDATE ... RETURNING
-- yields no task and the daemon never receives the embedded Radar prompt.
CREATE FUNCTION workspace_radar_task_is_authorized(candidate_task_id UUID)
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
  SELECT EXISTS (
    SELECT 1
    FROM agent_task_queue task
    JOIN agent_radar_run run
      ON run.task_id = task.id
     AND run.id::text = task.context->>'radar_run_id'
     AND run.agent_id = task.agent_id
    WHERE task.id = candidate_task_id
      AND task.context->>'type' = 'agent_radar'
      AND run.status IN ('planned', 'queued', 'running')
      AND (
        run.trigger_kind = 'manual'
        OR (
          run.trigger_kind = 'scheduled'
          AND run.cooldown_key = 'workspace_supervisor_radar'
          AND EXISTS (
            SELECT 1
            FROM workspace_radar_state state
            JOIN agent supervisor
              ON supervisor.workspace_id = state.workspace_id
             AND supervisor.id = state.supervisor_agent_id
            JOIN member owner_member
              ON owner_member.workspace_id = state.workspace_id
             AND owner_member.user_id = supervisor.owner_id
             AND owner_member.role = 'owner'
            WHERE state.workspace_id = run.workspace_id
              AND state.supervisor_agent_id = run.agent_id
              AND state.enabled
              AND supervisor.archived_at IS NULL
          )
        )
      )
  );
$$;

CREATE FUNCTION guard_workspace_radar_task_dispatch()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT workspace_radar_task_is_authorized(NEW.id) THEN
    RETURN NULL;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER trg_guard_workspace_radar_task_dispatch
BEFORE UPDATE OF status, dispatched_at ON agent_task_queue
FOR EACH ROW
WHEN (
  NEW.context->>'type' = 'agent_radar'
  AND NEW.status IN ('dispatched', 'running', 'waiting_local_directory')
)
EXECUTE FUNCTION guard_workspace_radar_task_dispatch();

-- Old replicas finalize a completed Radar run directly from running. New
-- completion handling first claims executing atomically with task completion.
-- Suppress old-pod success/no_action transitions so reconciliation records an
-- interrupted completion instead of falsely advancing workspace cadence.
CREATE FUNCTION guard_workspace_radar_run_success_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.trigger_kind = 'scheduled'
     AND NEW.cooldown_key = 'workspace_supervisor_radar'
     AND NEW.status IN ('succeeded', 'no_action')
     AND OLD.status <> 'executing' THEN
    RETURN NULL;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER trg_guard_workspace_radar_run_success_transition
BEFORE UPDATE OF status ON agent_radar_run
FOR EACH ROW
EXECUTE FUNCTION guard_workspace_radar_run_success_transition();

-- A pre-migration server executes Radar actions without re-checking the Wendy
-- binding.  Suppress its action INSERT as the last durable gate; the old
-- executor treats the missing RETURNING row like a deduplicated action and
-- therefore performs no visible side effect.
CREATE FUNCTION guard_workspace_radar_action_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM agent_radar_run run
    WHERE run.id = NEW.radar_run_id
      AND run.trigger_kind = 'scheduled'
  ) AND NOT EXISTS (
    SELECT 1
    FROM agent_radar_run run
    JOIN workspace_radar_state state
      ON state.workspace_id = run.workspace_id
     AND state.supervisor_agent_id = run.agent_id
     AND state.enabled
    JOIN agent supervisor
      ON supervisor.workspace_id = state.workspace_id
     AND supervisor.id = state.supervisor_agent_id
     AND supervisor.archived_at IS NULL
    JOIN member owner_member
      ON owner_member.workspace_id = state.workspace_id
     AND owner_member.user_id = supervisor.owner_id
     AND owner_member.role = 'owner'
    WHERE run.id = NEW.radar_run_id
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'executing'
      AND run.workspace_id = NEW.workspace_id
      AND run.agent_id = NEW.agent_id
      AND NEW.action_type IN ('no_action', 'comment_issue', 'mention_agent')
      -- New visible scheduled actions reserve their receipt as executing before
      -- writing the user-visible directive. A skipped receipt records a dedupe
      -- decision and has no side effect. Old executors insert approved rows;
      -- suppress those during a rolling deployment. no_action has no side effect.
      AND (
        NEW.action_type = 'no_action'
        OR NEW.status = 'executing'
        OR (
          NEW.status = 'skipped'
          AND EXISTS (
            SELECT 1
            FROM agent_radar_action existing
            JOIN agent_radar_run existing_run ON existing_run.id = existing.radar_run_id
            WHERE existing.workspace_id = NEW.workspace_id
              AND left(
                    existing.dedupe_key,
                    length(regexp_replace(NEW.dedupe_key, ':[^:]+$', '')) + 1
                  ) = regexp_replace(NEW.dedupe_key, ':[^:]+$', '') || ':'
              AND existing.status IN ('proposed', 'approved', 'executing', 'executed')
              AND existing.created_at > now() - interval '6 hours'
              AND existing_run.trigger_kind = 'scheduled'
              AND existing_run.cooldown_key = 'workspace_supervisor_radar'
          )
        )
      )
  ) THEN
    RETURN NULL;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER trg_guard_workspace_radar_action_insert
BEFORE INSERT ON agent_radar_action
FOR EACH ROW
EXECUTE FUNCTION guard_workspace_radar_action_insert();

-- Scheduled action keys use a stable prefix and must survive a Wendy rebind.
-- The historical per-agent index remains for manual/event dedupe; this guard
-- additionally prevents a replacement Wendy from repeating the same visible
-- directive in the same review window.
CREATE UNIQUE INDEX idx_agent_radar_action_workspace_supervisor_dedupe
  ON agent_radar_action(workspace_id, dedupe_key)
  WHERE dedupe_key LIKE 'workspace-supervisor:%'
    AND status IN ('proposed', 'approved', 'executing', 'executed');

-- A task completion can be retried after the client loses the HTTP response.
-- This receipt makes applying one run's success/failure to workspace state an
-- idempotent operation without adding a column to agent_radar_run (whose SELECT
-- * shape is shared by older generated clients during rolling deployment).
CREATE TABLE workspace_radar_run_state_ack (
  radar_run_id UUID PRIMARY KEY REFERENCES agent_radar_run(id) ON DELETE CASCADE,
  outcome TEXT NOT NULL CHECK (outcome IN ('succeeded', 'failed', 'cancelled')),
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Existing Wendy agents are user-scoped, so select only a Wendy owned by the
-- workspace owner. Ranking is deterministic and prefers an active, reachable,
-- canonical Wendy without changing any other user's personal Wendy.
WITH ranked_wendy AS (
  SELECT
    a.workspace_id,
    a.id AS agent_id,
    row_number() OVER (
      PARTITION BY a.workspace_id
      ORDER BY
        (a.archived_at IS NULL) DESC,
        (runtime.status = 'online') DESC NULLS LAST,
        (COALESCE(NULLIF(a.display_name, ''), a.name) = 'Wendy') DESC,
        (a.visibility = 'private') DESC,
        a.updated_at DESC,
        a.created_at DESC,
        a.id ASC
    ) AS candidate_rank
  FROM agent a
  JOIN member owner_member
    ON owner_member.workspace_id = a.workspace_id
   AND owner_member.user_id = a.owner_id
   AND owner_member.role = 'owner'
  LEFT JOIN agent_runtime runtime
    ON runtime.id = a.runtime_id
   AND runtime.workspace_id = a.workspace_id
  WHERE a.archived_at IS NULL
    AND COALESCE(NULLIF(a.display_name, ''), a.name) IN ('Wendy', 'Windy', 'Joe')
)
INSERT INTO workspace_radar_state (
  workspace_id,
  supervisor_agent_id,
  next_due_at
)
SELECT
  workspace_id,
  agent_id,
  now() + make_interval(secs => (
    (get_byte(uuid_send(workspace_id), 0)::integer * 256
      + get_byte(uuid_send(workspace_id), 1)::integer) % 1800
  ))
FROM ranked_wendy
WHERE candidate_rank = 1;

-- Stop legacy per-agent scheduled and event-driven Radar work immediately.
-- Without this repair, deployments would keep consuming already queued
-- automatic fan-out even though only the workspace Wendy may run proactively.
WITH unauthorized_runs AS MATERIALIZED (
  SELECT rr.id, rr.task_id
  FROM agent_radar_run rr
  WHERE rr.status IN ('planned', 'queued', 'running', 'executing')
    AND (
      rr.trigger_kind = 'event'
      OR (
        rr.trigger_kind = 'scheduled'
        AND rr.cooldown_key <> 'workspace_supervisor_radar'
      )
    )
), cancelled_tasks AS (
  UPDATE agent_task_queue task
  SET status = 'cancelled',
      completed_at = COALESCE(task.completed_at, now()),
      error = COALESCE(NULLIF(task.error, ''), 'Automatic Radar moved to the workspace Wendy supervisor'),
      failure_reason = 'radar_workspace_supervisor_migration'
  FROM unauthorized_runs unauthorized
  WHERE task.id = unauthorized.task_id
    AND task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
  RETURNING task.id
)
UPDATE agent_radar_run run
SET status = 'cancelled',
    error = CASE
      WHEN run.error = '' THEN 'Automatic Radar moved to the workspace Wendy supervisor'
      ELSE run.error
    END,
    finished_at = COALESCE(run.finished_at, now()),
    updated_at = now()
FROM unauthorized_runs unauthorized
WHERE run.id = unauthorized.id
  AND run.status IN ('planned', 'queued', 'running', 'executing');

-- The existing guard protects one active run per agent. This additional guard
-- protects the workspace responsibility when its Wendy binding changes.
CREATE UNIQUE INDEX idx_agent_radar_run_active_workspace_supervisor
  ON agent_radar_run(workspace_id)
  WHERE cooldown_key = 'workspace_supervisor_radar'
    AND status IN ('planned', 'queued', 'running', 'executing');

ALTER TABLE agent_radar_run
  VALIDATE CONSTRAINT agent_radar_run_active_scheduled_workspace_check;
