-- Keep the Research Attempt lifecycle aligned with the canonical Agent Inbox
-- execution lease. Dispatch attachment, provider start, cancellation request,
-- cancellation acknowledgement, and failure settlement are distinct facts.

ALTER TABLE research_task_attempt
  DROP CONSTRAINT research_task_attempt_status_check;

ALTER TABLE research_task_attempt
  ADD CONSTRAINT research_task_attempt_status_check
  CHECK (status IN ('dispatching', 'running', 'cancelling', 'succeeded', 'failed', 'cancelled', 'lost'));

ALTER TABLE research_task_attempt
  ADD COLUMN runtime_started_at TIMESTAMPTZ,
  ADD COLUMN runtime_last_observed_at TIMESTAMPTZ,
  ADD COLUMN runtime_lease_expires_at TIMESTAMPTZ,
  ADD COLUMN cancellation_requested_at TIMESTAMPTZ,
  ADD COLUMN pending_failure_class TEXT NOT NULL DEFAULT '',
  ADD COLUMN pending_failure_diagnostics TEXT NOT NULL DEFAULT '',
  ADD COLUMN pending_failure_retryable BOOLEAN NOT NULL DEFAULT false,
  ADD CONSTRAINT research_task_attempt_runtime_lease_observation_check
    CHECK (runtime_lease_expires_at IS NULL OR runtime_last_observed_at IS NOT NULL),
  ADD CONSTRAINT research_task_attempt_cancellation_order_check
    CHECK (
      cancellation_completed_at IS NULL
      OR cancellation_requested_at IS NULL
      OR cancellation_completed_at >= cancellation_requested_at
    ),
  ADD CONSTRAINT research_task_attempt_cancelling_failure_check
    CHECK (status <> 'cancelling' OR btrim(pending_failure_class) <> '');

DROP INDEX research_task_attempt_one_active_idx;
CREATE UNIQUE INDEX research_task_attempt_one_active_idx
  ON research_task_attempt (task_id)
  WHERE status IN ('dispatching', 'running', 'cancelling');

DROP INDEX research_task_attempt_cancellation_pending_idx;
CREATE INDEX research_task_attempt_cancellation_pending_idx
  ON research_task_attempt (session_id, dispatched_at, id)
  WHERE status IN ('cancelling', 'cancelled') AND cancellation_completed_at IS NULL;

CREATE OR REPLACE FUNCTION research_attempt_status_transition_allowed(old_status TEXT, new_status TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT old_status = new_status OR (old_status, new_status) IN (
    ('dispatching', 'running'),
    ('dispatching', 'cancelling'),
    ('dispatching', 'succeeded'),
    ('dispatching', 'failed'),
    ('dispatching', 'cancelled'),
    ('dispatching', 'lost'),
    ('running', 'cancelling'),
    ('running', 'succeeded'),
    ('running', 'failed'),
    ('running', 'cancelled'),
    ('running', 'lost'),
    ('cancelling', 'failed'),
    ('cancelling', 'cancelled'),
    ('cancelling', 'lost')
  )
$$;
