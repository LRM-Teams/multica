-- A deployment rollback cannot retain the v294-only cancelling state. Settle
-- those Attempts with their persisted timeout failure and keep the parent Task
-- retryable only when its attempt budget still allows another execution.
WITH settled AS (
  UPDATE research_task_attempt
  SET status = 'failed',
      failure_class = COALESCE(NULLIF(pending_failure_class, ''), 'migration_rollback'),
      diagnostics = COALESCE(NULLIF(pending_failure_diagnostics, ''), 'Attempt settled while rolling back migration 294.'),
      cancellation_completed_at = COALESCE(cancellation_completed_at, now()),
      completed_at = COALESCE(completed_at, now()),
      updated_at = now()
  WHERE status = 'cancelling'
  RETURNING task_id, attempt_number
)
UPDATE research_task task
SET status = CASE WHEN settled.attempt_number < task.max_attempts THEN 'ready' ELSE 'failed' END,
    ready_at = CASE WHEN settled.attempt_number < task.max_attempts THEN now() ELSE task.ready_at END,
    completed_at = CASE WHEN settled.attempt_number < task.max_attempts THEN NULL ELSE COALESCE(task.completed_at, now()) END,
    terminal_reason = CASE WHEN settled.attempt_number < task.max_attempts THEN '' ELSE 'attempt_budget_exhausted' END,
    assigned_agent_id = NULL,
    updated_at = now()
FROM settled
WHERE task.id = settled.task_id
  AND task.status IN ('dispatching', 'running');

CREATE OR REPLACE FUNCTION research_attempt_status_transition_allowed(old_status TEXT, new_status TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT old_status = new_status OR (old_status, new_status) IN (
    ('dispatching', 'running'),
    ('dispatching', 'succeeded'),
    ('dispatching', 'failed'),
    ('dispatching', 'cancelled'),
    ('dispatching', 'lost'),
    ('running', 'succeeded'),
    ('running', 'failed'),
    ('running', 'cancelled'),
    ('running', 'lost')
  )
$$;

DROP INDEX research_task_attempt_cancellation_pending_idx;
CREATE INDEX research_task_attempt_cancellation_pending_idx
  ON research_task_attempt (session_id, dispatched_at, id)
  WHERE status = 'cancelled' AND cancellation_completed_at IS NULL;

DROP INDEX research_task_attempt_one_active_idx;
CREATE UNIQUE INDEX research_task_attempt_one_active_idx
  ON research_task_attempt (task_id)
  WHERE status IN ('dispatching', 'running');

ALTER TABLE research_task_attempt
  DROP CONSTRAINT research_task_attempt_cancelling_failure_check,
  DROP CONSTRAINT research_task_attempt_cancellation_order_check,
  DROP CONSTRAINT research_task_attempt_runtime_lease_observation_check,
  DROP COLUMN pending_failure_retryable,
  DROP COLUMN pending_failure_diagnostics,
  DROP COLUMN pending_failure_class,
  DROP COLUMN cancellation_requested_at,
  DROP COLUMN runtime_lease_expires_at,
  DROP COLUMN runtime_last_observed_at,
  DROP COLUMN runtime_started_at;

ALTER TABLE research_task_attempt
  DROP CONSTRAINT research_task_attempt_status_check;

ALTER TABLE research_task_attempt
  ADD CONSTRAINT research_task_attempt_status_check
  CHECK (status IN ('dispatching', 'running', 'succeeded', 'failed', 'cancelled', 'lost'));
