-- This rollback reconstructs the old queue only for deployment rollback. New
-- application code never reads or writes it. Wake UUIDs remain stable so a
-- prior server image can resume from the reconstructed rows.

BEGIN;

DROP TRIGGER IF EXISTS task_group_manager_patrol_progress_trigger ON agent_inbox_event;
DROP TRIGGER IF EXISTS trg_guard_workspace_radar_task_dispatch ON agent_inbox_event;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_task ON agent_inbox_event;
DROP INDEX IF EXISTS idx_agent_inbox_event_agent_created;
DROP INDEX IF EXISTS idx_agent_inbox_event_issue_created;
DROP INDEX IF EXISTS idx_agent_inbox_event_runtime_ready;
DROP INDEX IF EXISTS idx_chat_message_task_user_created;

CREATE TABLE agent_task_queue (
  id UUID PRIMARY KEY,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  issue_id UUID REFERENCES issue(id) ON DELETE CASCADE,
  status TEXT NOT NULL
    CHECK (status IN ('queued', 'dispatched', 'running', 'completed', 'failed', 'cancelled', 'waiting_local_directory')),
  priority INTEGER NOT NULL DEFAULT 0,
  dispatched_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  result JSONB,
  error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  context JSONB,
  runtime_id UUID REFERENCES agent_runtime(id) ON DELETE CASCADE,
  session_id TEXT,
  work_dir TEXT,
  trigger_comment_id UUID REFERENCES comment(id) ON DELETE SET NULL,
  chat_session_id UUID REFERENCES chat_session(id) ON DELETE SET NULL,
  autopilot_run_id UUID REFERENCES autopilot_run(id) ON DELETE SET NULL,
  attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 3,
  parent_task_id UUID,
  failure_reason TEXT,
  trigger_summary TEXT,
  force_fresh_session BOOLEAN NOT NULL DEFAULT FALSE,
  is_leader_task BOOLEAN NOT NULL DEFAULT FALSE,
  wait_reason TEXT,
  initiator_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL
);

INSERT INTO agent_task_queue (
  id, agent_id, issue_id, status, priority, dispatched_at, started_at,
  completed_at, result, error, created_at, context, runtime_id, session_id,
  work_dir, trigger_comment_id, chat_session_id, autopilot_run_id, attempt,
  max_attempts, parent_task_id, failure_reason, trigger_summary,
  force_fresh_session, is_leader_task, wait_reason, initiator_user_id
)
SELECT
  event.id,
  event.agent_id,
  event.issue_id,
  CASE
    WHEN event.status = 'pending' THEN 'queued'
    WHEN event.status = 'draining' THEN 'dispatched'
    WHEN event.status = 'acked' AND event.terminal_outcome = 'failed' THEN 'failed'
    WHEN event.status = 'acked' THEN 'completed'
    WHEN event.status = 'suppressed' THEN 'cancelled'
    ELSE 'failed'
  END,
  event.priority,
  event.dispatched_at,
  event.started_at,
  COALESCE(event.completed_at, event.terminal_at),
  event.result,
  COALESCE(event.error, event.last_error),
  event.created_at,
  event.context,
  event.runtime_id,
  event.session_id,
  event.work_dir,
  event.trigger_comment_id,
  event.chat_session_id,
  event.autopilot_run_id,
  event.attempt,
  event.max_attempts,
  event.parent_task_id,
  event.failure_reason,
  event.trigger_summary,
  event.force_fresh_session,
  event.is_leader_task,
  event.wait_reason,
  event.initiator_user_id
FROM agent_inbox_event event
WHERE event.reason IN (
  'dm', 'channel_message', 'issue', 'quick_create', 'autopilot',
  'agent_radar', 'training', 'environment_dispatch', 'memory_curation',
  'reminder'
);

ALTER TABLE agent_task_queue
  ADD CONSTRAINT agent_task_queue_parent_task_id_fkey
  FOREIGN KEY (parent_task_id) REFERENCES agent_task_queue(id) ON DELETE SET NULL;

-- Only foreign keys marked by the up migration belonged to the retired queue.
-- Native inbox delivery/session constraints stay on agent_inbox_event.
DO $$
DECLARE
  fk RECORD;
  replacement_definition TEXT;
BEGIN
  FOR fk IN
    SELECT
      constraint_row.conrelid::regclass AS relation_name,
      constraint_row.conname,
      pg_get_constraintdef(constraint_row.oid) AS definition
    FROM pg_constraint constraint_row
    WHERE constraint_row.contype = 'f'
      AND constraint_row.confrelid = 'agent_inbox_event'::regclass
      AND obj_description(constraint_row.oid, 'pg_constraint') =
        'agent_wake_clean_cutover_223'
  LOOP
    replacement_definition := replace(
      fk.definition,
      'REFERENCES agent_inbox_event(id)',
      'REFERENCES agent_task_queue(id)'
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
  END LOOP;
END $$;

CREATE OR REPLACE FUNCTION journal_workspace_radar_task()
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

CREATE OR REPLACE FUNCTION journal_workspace_radar_task_progress()
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

CREATE OR REPLACE FUNCTION workspace_radar_task_is_authorized(candidate_task_id UUID)
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

CREATE TRIGGER trg_guard_workspace_radar_task_dispatch
BEFORE UPDATE OF status, dispatched_at ON agent_task_queue
FOR EACH ROW
WHEN (
  NEW.context->>'type' = 'agent_radar'
  AND NEW.status IN ('dispatched', 'running', 'waiting_local_directory')
)
EXECUTE FUNCTION guard_workspace_radar_task_dispatch();

DO $$
DECLARE
  definition TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'refresh_workspace_radar_time_signals(uuid,timestamp with time zone)'::regprocedure
  ) INTO definition;
  definition := replace(definition, 'agent_inbox_event', 'agent_task_queue');
  definition := replace(
    definition,
    'task.status IN (''pending'', ''draining'', ''failed'')',
    'task.status IN (''queued'', ''dispatched'', ''running'', ''waiting_local_directory'')'
  );
  EXECUTE definition;
END $$;

CREATE TRIGGER task_group_manager_patrol_progress_trigger
AFTER INSERT OR UPDATE OF status
ON agent_task_queue
FOR EACH ROW
WHEN (NEW.issue_id IS NOT NULL)
EXECUTE FUNCTION refresh_group_manager_patrol_from_issue_child();

CREATE INDEX idx_agent_task_queue_agent
  ON agent_task_queue(agent_id, status);
CREATE INDEX idx_agent_task_queue_runtime_ready
  ON agent_task_queue(runtime_id, created_at, id)
  WHERE status = 'queued';

ALTER TABLE agent_execution
  DROP CONSTRAINT IF EXISTS agent_execution_source_kind_check;
ALTER TABLE agent_execution
  ADD CONSTRAINT agent_execution_source_kind_check
  CHECK (source_kind IN ('queue', 'inbox'));
UPDATE agent_execution
SET source_kind = 'queue'
WHERE source_event_id IN (SELECT id FROM agent_task_queue);

-- These are the canonical work wakes copied back into the reconstructed
-- queue. Remove their inbox/delivery copies so an older server cannot run the
-- same wake through both schedulers after rollback.
DELETE FROM agent_inbox_event
WHERE id IN (SELECT id FROM agent_task_queue);

ALTER TABLE agent_execution
  DROP COLUMN status,
  DROP COLUMN result,
  DROP COLUMN error,
  DROP COLUMN failure_reason,
  DROP COLUMN completed_at;

ALTER TABLE agent_inbox_event
  ALTER COLUMN attempt SET DEFAULT 0,
  DROP CONSTRAINT IF EXISTS agent_inbox_event_parent_task_id_fkey,
  DROP COLUMN issue_id,
  DROP COLUMN source_chat_message_id,
  DROP COLUMN context,
  DROP COLUMN dispatched_at,
  DROP COLUMN started_at,
  DROP COLUMN completed_at,
  DROP COLUMN result,
  DROP COLUMN error,
  DROP COLUMN session_id,
  DROP COLUMN work_dir,
  DROP COLUMN trigger_comment_id,
  DROP COLUMN autopilot_run_id,
  DROP COLUMN max_attempts,
  DROP COLUMN parent_task_id,
  DROP COLUMN failure_reason,
  DROP COLUMN trigger_summary,
  DROP COLUMN force_fresh_session,
  DROP COLUMN is_leader_task,
  DROP COLUMN wait_reason,
  DROP COLUMN initiator_user_id;

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
    'channel_onboarding'
  ));

-- Task #100 (2026-08-02): this narrowing drops 'completed' and 'cancelled'
-- from terminal_outcome with no remap — none of the remaining values
-- (replied/no_reply/held/failed/sent/skipped/expired) mean "finished
-- successfully" or "was cancelled"; remapping to any of them would fabricate
-- a terminal state that never happened. There is no safe remap target for
-- either value. Fail loud instead of letting ALTER TABLE...ADD CONSTRAINT
-- bounce off a raw Postgres constraint-violation error, matching migrations
-- 107/143/181/182/186/207/247/254/268's fix (tasks #99/#101).
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count
      FROM agent_inbox_event WHERE terminal_outcome IN ('completed', 'cancelled');
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 223 down cannot proceed: % row(s) in agent_inbox_event have terminal_outcome in (''completed'', ''cancelled''). There is no safe value to remap them to under the narrower terminal_outcome list this migration is reverting to — none of the remaining values mean "finished successfully" or "was cancelled". If you accept permanently losing this outcome history, run: DELETE FROM agent_inbox_event WHERE terminal_outcome IN (''completed'', ''cancelled''); -- then re-run this down migration.', affected_count;
    END IF;
END $$;

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
    'expired'
  ));

DROP FUNCTION IF EXISTS ensure_agent_wake_session(UUID);
DROP INDEX IF EXISTS agent_session_agent_scope_unique;
DELETE FROM agent_session WHERE scope = 'agent';
ALTER TABLE agent_session
  DROP CONSTRAINT IF EXISTS agent_session_scope_check;
ALTER TABLE agent_session
  ADD CONSTRAINT agent_session_scope_check
  CHECK (scope IN ('channel', 'dm', 'direct_chat'));

COMMIT;
