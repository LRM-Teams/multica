-- The agent inbox is the only wake scheduler after this migration. Queue-era
-- rows are copied with their original UUIDs so every existing run/message/audit
-- reference keeps pointing at the same logical wake. Active rows are deliberately
-- re-enqueued: deployment restart is the cutover boundary and no old lease is
-- allowed to survive it.

BEGIN;

ALTER TABLE agent_session
  DROP CONSTRAINT IF EXISTS agent_session_scope_check;
ALTER TABLE agent_session
  ADD CONSTRAINT agent_session_scope_check
  CHECK (scope IN ('channel', 'dm', 'direct_chat', 'agent'));

CREATE UNIQUE INDEX agent_session_agent_scope_unique
  ON agent_session(workspace_id, agent_id)
  WHERE scope = 'agent';

CREATE OR REPLACE FUNCTION ensure_agent_wake_session(candidate_agent_id UUID)
RETURNS UUID
LANGUAGE plpgsql
AS $$
DECLARE
  wake_session_id UUID;
BEGIN
  INSERT INTO agent_session (
    workspace_id,
    agent_id,
    runtime_id,
    scope,
    status
  )
  SELECT
    agent.workspace_id,
    agent.id,
    agent.runtime_id,
    'agent',
    'active'
  FROM agent
  WHERE agent.id = candidate_agent_id
  ON CONFLICT (workspace_id, agent_id)
    WHERE scope = 'agent'
  DO UPDATE SET
    runtime_id = EXCLUDED.runtime_id,
    status = 'active',
    updated_at = now()
  RETURNING id INTO wake_session_id;

  RETURN wake_session_id;
END;
$$;

INSERT INTO agent_session (
  workspace_id,
  agent_id,
  runtime_id,
  scope,
  status
)
SELECT DISTINCT
  agent.workspace_id,
  task.agent_id,
  agent.runtime_id,
  'agent',
  'active'
FROM agent_task_queue task
JOIN agent ON agent.id = task.agent_id
ON CONFLICT (workspace_id, agent_id)
  WHERE scope = 'agent'
DO UPDATE SET
  runtime_id = EXCLUDED.runtime_id,
  status = 'active',
  updated_at = now();

ALTER TABLE agent_inbox_event
  ADD COLUMN issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
  ADD COLUMN source_chat_message_id UUID REFERENCES chat_message(id) ON DELETE SET NULL,
  ADD COLUMN context JSONB,
  ADD COLUMN dispatched_at TIMESTAMPTZ,
  ADD COLUMN started_at TIMESTAMPTZ,
  ADD COLUMN completed_at TIMESTAMPTZ,
  ADD COLUMN result JSONB,
  ADD COLUMN error TEXT,
  ADD COLUMN session_id TEXT,
  ADD COLUMN work_dir TEXT,
  ADD COLUMN trigger_comment_id UUID REFERENCES comment(id) ON DELETE SET NULL,
  ADD COLUMN autopilot_run_id UUID REFERENCES autopilot_run(id) ON DELETE SET NULL,
  ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
  ADD COLUMN parent_task_id UUID,
  ADD COLUMN failure_reason TEXT,
  ADD COLUMN trigger_summary TEXT,
  ADD COLUMN force_fresh_session BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN is_leader_task BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN wait_reason TEXT,
  ADD COLUMN initiator_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL;

-- Canonical provider attempts are one-based. Transport deliveries may be
-- renewed or reclaimed without consuming this logical retry budget.
ALTER TABLE agent_inbox_event
  ALTER COLUMN attempt SET DEFAULT 1;

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;
ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention',
    'dm',
    'ambient',
    'thread_reply',
    'channel_message',
    'collaboration_turn',
    'collaboration_manager_fallback',
    'channel_onboarding',
    'issue',
    'quick_create',
    'autopilot',
    'agent_radar',
    'training',
    'environment_dispatch',
    'memory_curation',
    'reminder'
  ));

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_terminal_outcome_check;
ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_terminal_outcome_check
  CHECK (terminal_outcome IN (
    'replied',
    'no_reply',
    'held',
    'failed',
    'sent',
    'skipped',
    'expired',
    'completed',
    'cancelled'
  ));

-- A real provider start creates one immutable execution. Existing rows already
-- cover all usage-bearing queue runs; the additional outcome columns make this
-- the thin run record used after the queue table is removed.
ALTER TABLE agent_execution
  ADD COLUMN status TEXT NOT NULL DEFAULT 'running'
    CHECK (status IN ('running', 'completed', 'failed', 'cancelled')),
  ADD COLUMN result JSONB,
  ADD COLUMN error TEXT,
  ADD COLUMN failure_reason TEXT,
  ADD COLUMN completed_at TIMESTAMPTZ;

-- The queue backfill resolves the earliest user message for every historical
-- task. Production has hundreds of thousands of tasks and messages, so the
-- lateral lookup must not scan the entire chat_message table once per task.
CREATE INDEX IF NOT EXISTS idx_chat_message_task_user_created
  ON chat_message(task_id, created_at, id)
  WHERE role = 'user';

-- Copy every historical/active queue row into the canonical wake table. The
-- source UUID is the idempotency key. If a previous interrupted/manual cutover
-- already copied a row, the source fields are refreshed but an existing inbox
-- terminal fact is never reopened.
INSERT INTO agent_inbox_event (
  id,
  workspace_id,
  agent_session_id,
  conversation_id,
  channel_id,
  chat_session_id,
  agent_id,
  runtime_id,
  execution_config,
  source_chat_message_id,
  reason,
  requires_wake,
  status,
  priority,
  seq_from,
  seq_to,
  attempt,
  last_error,
  claimed_at,
  acked_at,
  created_at,
  updated_at,
  terminal_outcome,
  retryable,
  terminal_at,
  issue_id,
  context,
  dispatched_at,
  started_at,
  completed_at,
  result,
  error,
  session_id,
  work_dir,
  trigger_comment_id,
  autopilot_run_id,
  max_attempts,
  parent_task_id,
  failure_reason,
  trigger_summary,
  force_fresh_session,
  is_leader_task,
  wait_reason,
  initiator_user_id
)
SELECT
  task.id,
  agent.workspace_id,
  COALESCE(chat_delivery_session.id, agent_delivery_session.id),
  chat_delivery_session.conversation_id,
  chat_delivery_session.channel_id,
  task.chat_session_id,
  task.agent_id,
  task.runtime_id,
  task.context,
  source_chat_message.id,
  CASE
    WHEN task.chat_session_id IS NOT NULL AND chat_delivery_session.channel_id IS NOT NULL
      THEN 'channel_message'
    WHEN task.chat_session_id IS NOT NULL
      THEN 'dm'
    WHEN task.context->>'type' = 'quick_create'
      THEN 'quick_create'
    WHEN task.context->>'type' = 'agent_radar'
      THEN 'agent_radar'
    WHEN task.autopilot_run_id IS NOT NULL
      THEN 'autopilot'
    WHEN task.context ? 'critic_of'
      THEN 'training'
    ELSE 'issue'
  END,
  task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory'),
  CASE
    WHEN task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory') THEN 'pending'
    WHEN task.status IN ('completed', 'failed') THEN 'acked'
    ELSE 'suppressed'
  END,
  task.priority,
  0,
  0,
  task.attempt,
  task.error,
  NULL,
  CASE
    WHEN task.status IN ('completed', 'failed', 'cancelled') THEN task.completed_at
    ELSE NULL
  END,
  task.created_at,
  now(),
  CASE
    WHEN task.status = 'completed' THEN 'completed'
    WHEN task.status = 'failed' THEN 'failed'
    WHEN task.status = 'cancelled' THEN 'cancelled'
    ELSE NULL
  END,
  FALSE,
  CASE
    WHEN task.status IN ('completed', 'failed', 'cancelled') THEN task.completed_at
    ELSE NULL
  END,
  task.issue_id,
  task.context,
  task.dispatched_at,
  task.started_at,
  task.completed_at,
  task.result,
  task.error,
  task.session_id,
  task.work_dir,
  task.trigger_comment_id,
  task.autopilot_run_id,
  task.max_attempts,
  task.parent_task_id,
  task.failure_reason,
  task.trigger_summary,
  task.force_fresh_session,
  task.is_leader_task,
  task.wait_reason,
  task.initiator_user_id
FROM agent_task_queue task
JOIN agent ON agent.id = task.agent_id
JOIN agent_session agent_delivery_session
  ON agent_delivery_session.workspace_id = agent.workspace_id
 AND agent_delivery_session.agent_id = task.agent_id
 AND agent_delivery_session.scope = 'agent'
LEFT JOIN LATERAL (
  SELECT session.*
  FROM agent_session session
  WHERE session.workspace_id = agent.workspace_id
    AND session.agent_id = task.agent_id
    AND session.chat_session_id = task.chat_session_id
  ORDER BY session.updated_at DESC, session.id
  LIMIT 1
) chat_delivery_session ON task.chat_session_id IS NOT NULL
LEFT JOIN LATERAL (
  SELECT message.id
  FROM chat_message message
  WHERE message.task_id = task.id
    AND message.role = 'user'
  ORDER BY message.created_at, message.id
  LIMIT 1
) source_chat_message ON TRUE
ON CONFLICT (id) DO UPDATE SET
  issue_id = COALESCE(agent_inbox_event.issue_id, EXCLUDED.issue_id),
  source_chat_message_id = COALESCE(agent_inbox_event.source_chat_message_id, EXCLUDED.source_chat_message_id),
  context = COALESCE(agent_inbox_event.context, EXCLUDED.context),
  trigger_comment_id = COALESCE(agent_inbox_event.trigger_comment_id, EXCLUDED.trigger_comment_id),
  autopilot_run_id = COALESCE(agent_inbox_event.autopilot_run_id, EXCLUDED.autopilot_run_id),
  parent_task_id = COALESCE(agent_inbox_event.parent_task_id, EXCLUDED.parent_task_id),
  trigger_summary = COALESCE(agent_inbox_event.trigger_summary, EXCLUDED.trigger_summary),
  initiator_user_id = COALESCE(agent_inbox_event.initiator_user_id, EXCLUDED.initiator_user_id),
  updated_at = now();

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_parent_task_id_fkey
  FOREIGN KEY (parent_task_id) REFERENCES agent_inbox_event(id) ON DELETE SET NULL;
COMMENT ON CONSTRAINT agent_inbox_event_parent_task_id_fkey ON agent_inbox_event
  IS 'agent_wake_clean_cutover_223';

-- Preserve every provider run, including executions that predate usage rows.
-- Pending rows have not started a provider and intentionally do not manufacture
-- an execution record.
INSERT INTO agent_execution (
  id,
  source_kind,
  source_event_id,
  source,
  workspace_id,
  runtime_id,
  agent_id,
  chat_session_id,
  issue_id,
  project_id,
  execution_config,
  started_at,
  created_at,
  status,
  result,
  error,
  failure_reason,
  completed_at
)
SELECT
  task.id,
  'inbox',
  task.id,
  CASE WHEN task.issue_id IS NULL THEN 'chat' ELSE 'issue' END,
  agent.workspace_id,
  task.runtime_id,
  task.agent_id,
  task.chat_session_id,
  task.issue_id,
  issue.project_id,
  task.context,
  COALESCE(task.started_at, task.dispatched_at, task.created_at),
  task.created_at,
  CASE task.status
    WHEN 'completed' THEN 'completed'
    WHEN 'failed' THEN 'failed'
    WHEN 'cancelled' THEN 'cancelled'
    ELSE 'running'
  END,
  task.result,
  task.error,
  task.failure_reason,
  CASE
    WHEN task.status IN ('completed', 'failed', 'cancelled') THEN task.completed_at
    ELSE NULL
  END
FROM agent_task_queue task
JOIN agent ON agent.id = task.agent_id
LEFT JOIN issue ON issue.id = task.issue_id
WHERE task.started_at IS NOT NULL
   OR task.status IN ('completed', 'failed', 'cancelled')
ON CONFLICT (id) DO UPDATE SET
  source_kind = 'inbox',
  source_event_id = EXCLUDED.source_event_id,
  status = EXCLUDED.status,
  result = EXCLUDED.result,
  error = EXCLUDED.error,
  failure_reason = EXCLUDED.failure_reason,
  completed_at = EXCLUDED.completed_at;

UPDATE agent_execution
SET source_kind = 'inbox'
WHERE source_kind = 'queue';

ALTER TABLE agent_execution
  DROP CONSTRAINT IF EXISTS agent_execution_source_kind_check;
ALTER TABLE agent_execution
  ADD CONSTRAINT agent_execution_source_kind_check
  CHECK (source_kind = 'inbox');

-- Every dependent UUID keeps its original logical meaning, but now references
-- the canonical wake row. This covers run messages, Activity, Radar, Reminder,
-- transport audit/drafts, Lark, memory evidence, environment dispatch, and any
-- future FK already present when the migration runs.
DO $$
DECLARE
  fk RECORD;
  replacement_definition TEXT;
BEGIN
  FOR fk IN
    SELECT
      constraint_row.oid,
      constraint_row.conrelid::regclass AS relation_name,
      constraint_row.conname,
      pg_get_constraintdef(constraint_row.oid) AS definition
    FROM pg_constraint constraint_row
    WHERE constraint_row.contype = 'f'
      AND constraint_row.confrelid = 'agent_task_queue'::regclass
  LOOP
    replacement_definition := replace(
      fk.definition,
      'REFERENCES agent_task_queue(id)',
      'REFERENCES agent_inbox_event(id)'
    );
    EXECUTE format(
      'ALTER TABLE %s DROP CONSTRAINT %I',
      fk.relation_name,
      fk.conname
    );
    EXECUTE format(
      'ALTER TABLE %s ADD CONSTRAINT %I %s',
      fk.relation_name,
      fk.conname,
      replacement_definition
    );
    EXECUTE format(
      'COMMENT ON CONSTRAINT %I ON %s IS %L',
      fk.conname,
      fk.relation_name,
      'agent_wake_clean_cutover_223'
    );
  END LOOP;
END $$;

-- Workspace Radar observes issue work progress from the canonical wake/event
-- row. Chat wakes and Radar's own internal run stay excluded exactly as before.
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_task ON agent_task_queue;
DROP TRIGGER IF EXISTS trg_guard_workspace_radar_task_dispatch ON agent_task_queue;

CREATE OR REPLACE FUNCTION journal_workspace_radar_task()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent_inbox_event%ROWTYPE;
  directive workspace_radar_directive_artifact%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  IF row_value.chat_session_id IS NOT NULL
     OR COALESCE(row_value.context->>'type', '') = 'agent_radar' THEN
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
        'task', row_value.id, row_value.workspace_id,
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
     AND row_value.status IN ('pending', 'draining') THEN
    RETURN NEW;
  END IF;

  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'task', row_value.id, clock_timestamp(),
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
AFTER INSERT OR UPDATE OR DELETE ON agent_inbox_event
FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_task();

CREATE OR REPLACE FUNCTION journal_workspace_radar_task_progress()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent_task_progress_snapshot%ROWTYPE;
  task_row agent_inbox_event%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  SELECT event.* INTO task_row
  FROM agent_inbox_event event
  WHERE event.id = row_value.task_id;
  IF task_row.id IS NULL
     OR task_row.chat_session_id IS NOT NULL
     OR COALESCE(task_row.context->>'type', '') = 'agent_radar' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  PERFORM record_workspace_radar_change(
    task_row.workspace_id, 'task_progress', row_value.task_id, clock_timestamp(),
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
  WHERE workspace_id = task_row.workspace_id
    AND signal_kind = 'stale_task'
    AND entity_id = row_value.task_id;
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE OR REPLACE FUNCTION workspace_radar_task_is_authorized(candidate_task_id UUID)
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
  SELECT EXISTS (
    SELECT 1
    FROM agent_inbox_event event
    JOIN agent_radar_run run
      ON run.task_id = event.id
     AND run.id::text = event.context->>'radar_run_id'
     AND run.agent_id = event.agent_id
    WHERE event.id = candidate_task_id
      AND event.context->>'type' = 'agent_radar'
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

CREATE TRIGGER trg_guard_workspace_radar_task_dispatch
BEFORE UPDATE OF status, claimed_at ON agent_inbox_event
FOR EACH ROW
WHEN (
  NEW.context->>'type' = 'agent_radar'
  AND NEW.status = 'draining'
)
EXECUTE FUNCTION guard_workspace_radar_task_dispatch();

-- The stale-work Radar scanner is a PL/pgSQL function, so PostgreSQL stores
-- the table name in its function body instead of maintaining a relation
-- dependency. Rewrite that body explicitly; otherwise the table can be
-- dropped while the next Radar scan still fails at runtime.
DO $$
DECLARE
  definition TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'refresh_workspace_radar_time_signals(uuid,timestamp with time zone)'::regprocedure
  ) INTO definition;
  definition := replace(definition, 'agent_task_queue', 'agent_inbox_event');
  definition := replace(
    definition,
    'task.status IN (''queued'', ''dispatched'', ''running'', ''waiting_local_directory'')',
    'task.status IN (''pending'', ''draining'', ''failed'')'
  );
  EXECUTE definition;
END $$;

-- PR #1153 originally observes queue task progress. Rebind it when present;
-- the conditional keeps this migration executable on databases where that
-- feature was never installed.
DROP TRIGGER IF EXISTS task_group_manager_patrol_progress_trigger ON agent_task_queue;
DO $$
BEGIN
  IF to_regprocedure('refresh_group_manager_patrol_from_issue_child()') IS NOT NULL THEN
    EXECUTE $trigger$
      CREATE TRIGGER task_group_manager_patrol_progress_trigger
      AFTER INSERT OR UPDATE OF status
      ON agent_inbox_event
      FOR EACH ROW
      WHEN (NEW.issue_id IS NOT NULL)
      EXECUTE FUNCTION refresh_group_manager_patrol_from_issue_child()
    $trigger$;
  END IF;
END $$;

DROP TABLE agent_task_queue;

CREATE INDEX idx_agent_inbox_event_agent_created
  ON agent_inbox_event(agent_id, created_at DESC, id DESC);
CREATE INDEX idx_agent_inbox_event_issue_created
  ON agent_inbox_event(issue_id, created_at DESC, id DESC)
  WHERE issue_id IS NOT NULL;
CREATE INDEX idx_agent_inbox_event_runtime_ready
  ON agent_inbox_event(runtime_id, created_at, id)
  WHERE status IN ('pending', 'failed');

COMMIT;
