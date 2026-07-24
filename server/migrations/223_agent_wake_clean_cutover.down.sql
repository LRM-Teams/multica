-- This rollback reconstructs the old queue only for deployment rollback. New
-- application code never reads or writes it. Wake UUIDs remain stable so a
-- prior server image can resume from the reconstructed rows.

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

ALTER TABLE agent_execution
  DROP COLUMN status,
  DROP COLUMN result,
  DROP COLUMN error,
  DROP COLUMN failure_reason,
  DROP COLUMN completed_at;

ALTER TABLE agent_inbox_event
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
