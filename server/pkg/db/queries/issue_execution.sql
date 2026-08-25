-- Canonical Goal -> Issue -> Run persistence contracts. These queries are
-- additive until IssueExecutionReconciler replaces the legacy enqueue paths.

-- name: GetIssueExecutionStateForUpdate :one
SELECT
  id,
  workspace_id,
  status,
  assignee_type,
  assignee_id,
  channel_goal_id,
  goal_required,
  execution_revision,
  execution_attempt_sequence,
  acceptance_criteria,
  updated_at
FROM issue
WHERE id = @issue_id
  AND workspace_id = @workspace_id
FOR UPDATE;

-- name: AdvanceIssueExecutionRevision :one
UPDATE issue
SET execution_revision = execution_revision + 1,
    updated_at = now()
WHERE id = @issue_id
  AND workspace_id = @workspace_id
  AND execution_revision = @expected_execution_revision
RETURNING execution_revision, execution_attempt_sequence, updated_at;

-- name: AllocateIssueExecutionAttempt :one
UPDATE issue
SET execution_attempt_sequence = execution_attempt_sequence + 1,
    updated_at = now()
WHERE id = @issue_id
  AND workspace_id = @workspace_id
  AND execution_revision = @expected_execution_revision
RETURNING execution_revision, execution_attempt_sequence AS attempt_number, updated_at;

-- name: CreateActiveIssueExecution :one
INSERT INTO active_issue_execution (
  workspace_id,
  issue_id,
  run_id,
  agent_id,
  issue_execution_revision,
  attempt_number,
  status
)
VALUES (
  @workspace_id,
  @issue_id,
  @run_id,
  @agent_id,
  @issue_execution_revision,
  @attempt_number,
  'dispatching'
)
RETURNING *;

-- name: GetActiveIssueExecution :one
SELECT *
FROM active_issue_execution
WHERE workspace_id = @workspace_id
  AND issue_id = @issue_id;

-- name: GetActiveIssueExecutionByRun :one
SELECT *
FROM active_issue_execution
WHERE workspace_id = @workspace_id
  AND run_id = @run_id;

-- name: ActivateIssueExecution :one
UPDATE active_issue_execution
SET status = 'active',
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND issue_id = @issue_id
  AND run_id = @run_id
  AND issue_execution_revision = @issue_execution_revision
  AND status = 'dispatching'
RETURNING *;

-- name: BeginReleaseIssueExecution :one
UPDATE active_issue_execution
SET status = 'releasing',
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND issue_id = @issue_id
  AND run_id = @run_id
  AND status IN ('dispatching', 'active')
RETURNING *;

-- name: DeleteReleasedIssueExecution :execrows
DELETE FROM active_issue_execution
WHERE workspace_id = @workspace_id
  AND issue_id = @issue_id
  AND run_id = @run_id
  AND status = 'releasing';

-- name: CreateIssueDispatchOutbox :one
INSERT INTO issue_dispatch_outbox (
  workspace_id,
  issue_id,
  run_id,
  agent_id,
  issue_execution_revision,
  attempt_number,
  dispatch_key,
  trigger_kind,
  request_payload,
  request_hash
)
VALUES (
  @workspace_id,
  @issue_id,
  @run_id,
  @agent_id,
  @issue_execution_revision,
  @attempt_number,
  @dispatch_key,
  @trigger_kind,
  @request_payload,
  @request_hash
)
ON CONFLICT (dispatch_key) DO UPDATE
SET dispatch_key = EXCLUDED.dispatch_key
WHERE issue_dispatch_outbox.workspace_id = EXCLUDED.workspace_id
  AND issue_dispatch_outbox.issue_id = EXCLUDED.issue_id
  AND issue_dispatch_outbox.run_id = EXCLUDED.run_id
  AND issue_dispatch_outbox.agent_id = EXCLUDED.agent_id
  AND issue_dispatch_outbox.issue_execution_revision = EXCLUDED.issue_execution_revision
  AND issue_dispatch_outbox.attempt_number = EXCLUDED.attempt_number
  AND issue_dispatch_outbox.trigger_kind = EXCLUDED.trigger_kind
  AND issue_dispatch_outbox.request_hash = EXCLUDED.request_hash
RETURNING *;

-- name: GetIssueDispatchOutboxByKey :one
SELECT *
FROM issue_dispatch_outbox
WHERE workspace_id = @workspace_id
  AND dispatch_key = @dispatch_key;

-- name: ClaimIssueDispatchOutbox :many
WITH candidates AS (
  SELECT candidate.id
  FROM issue_dispatch_outbox AS candidate
  WHERE candidate.workspace_id = @workspace_id
    AND (
      (candidate.status = 'pending' AND candidate.next_delivery_at <= now())
      OR (candidate.status = 'delivering' AND candidate.lease_expires_at <= now())
    )
  ORDER BY candidate.next_delivery_at ASC, candidate.created_at ASC, candidate.id ASC
  FOR UPDATE SKIP LOCKED
  LIMIT @claim_limit
)
UPDATE issue_dispatch_outbox AS outbox
SET status = 'delivering',
    delivery_attempts = outbox.delivery_attempts + 1,
    lease_token = @lease_token,
    lease_expires_at = @lease_expires_at,
    last_error = '',
    updated_at = now()
FROM candidates
WHERE outbox.id = candidates.id
RETURNING outbox.*;

-- name: MarkIssueDispatchOutboxDelivered :one
UPDATE issue_dispatch_outbox
SET status = 'delivered',
    lease_token = NULL,
    lease_expires_at = NULL,
    delivered_event_id = run_id,
    delivered_at = now(),
    last_error = '',
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND id = @outbox_id
  AND run_id = @run_id
  AND status = 'delivering'
  AND lease_token = @lease_token
RETURNING *;

-- name: RescheduleIssueDispatchOutbox :one
UPDATE issue_dispatch_outbox
SET status = 'pending',
    lease_token = NULL,
    lease_expires_at = NULL,
    next_delivery_at = @next_delivery_at,
    last_error = @last_error,
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND id = @outbox_id
  AND status = 'delivering'
  AND lease_token = @lease_token
RETURNING *;

-- name: FailIssueDispatchOutbox :one
UPDATE issue_dispatch_outbox
SET status = 'failed',
    lease_token = NULL,
    lease_expires_at = NULL,
    last_error = @last_error,
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND id = @outbox_id
  AND status = 'delivering'
  AND lease_token = @lease_token
RETURNING *;

-- name: CancelIssueDispatchOutbox :one
UPDATE issue_dispatch_outbox
SET status = 'cancelled',
    lease_token = NULL,
    lease_expires_at = NULL,
    last_error = @reason,
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND issue_id = @issue_id
  AND run_id = @run_id
  AND status IN ('pending', 'delivering', 'failed')
RETURNING *;

-- name: BindCanonicalIssueRunEvent :one
UPDATE agent_inbox_event AS event
SET issue_run_kind = 'canonical',
    issue_execution_revision = claim.issue_execution_revision,
    issue_execution_attempt_number = claim.attempt_number,
    updated_at = now()
FROM active_issue_execution AS claim
WHERE event.id = @run_id
  AND event.workspace_id = @workspace_id
  AND event.issue_id = claim.issue_id
  AND event.agent_id = claim.agent_id
  AND event.reason = 'issue'
  AND claim.workspace_id = @workspace_id
  AND claim.run_id = @run_id
  AND claim.status = 'dispatching'
  AND event.issue_run_kind IS NULL
RETURNING event.*;

-- name: GetCanonicalIssueRunEvent :one
SELECT *
FROM agent_inbox_event
WHERE workspace_id = @workspace_id
  AND id = @run_id
  AND issue_run_kind = 'canonical';

-- name: HasIncompleteIssueExecutionDependencies :one
SELECT EXISTS (
  SELECT 1
  FROM issue_dependency dependency
  JOIN issue upstream ON upstream.id = dependency.depends_on_issue_id
  WHERE dependency.issue_id = @issue_id
    AND dependency.type = 'blocked_by'
    AND upstream.status NOT IN ('done', 'cancelled')
);

-- name: CancelIssueDispatchOutboxesForIssue :many
UPDATE issue_dispatch_outbox
SET status = 'cancelled',
    lease_token = NULL,
    lease_expires_at = NULL,
    last_error = @reason,
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND issue_id = @issue_id
  AND status IN ('pending', 'delivering', 'failed')
RETURNING *;

-- name: CancelSupersededIssueRunEvents :many
UPDATE agent_inbox_event
SET status = 'suppressed',
    terminal_outcome = COALESCE(terminal_outcome, 'cancelled'),
    terminal_at = COALESCE(terminal_at, now()),
    completed_at = COALESCE(completed_at, now()),
    last_error = @reason,
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND issue_id = @issue_id
  AND reason = 'issue'
  AND trigger_comment_id IS NULL
  AND (sqlc.narg('keep_run_id')::uuid IS NULL OR id <> sqlc.narg('keep_run_id')::uuid)
  AND status IN ('pending', 'failed', 'draining', 'running')
RETURNING *;

-- name: DeleteActiveIssueExecutionForIssue :execrows
DELETE FROM active_issue_execution
WHERE workspace_id = @workspace_id
  AND issue_id = @issue_id;

-- name: ReleaseExecutorWorkOwnerLeaseForHandoff :execrows
UPDATE work_owner_lease
SET status = 'released',
    handoff_to = sqlc.narg('handoff_to'),
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND issue_id = @issue_id
  AND role = 'executor'
  AND status = 'active'
  AND (sqlc.narg('keep_owner_agent_id')::uuid IS NULL
       OR owner_agent_id <> sqlc.narg('keep_owner_agent_id')::uuid);

-- name: CreateCanonicalIssueRunEvent :one
WITH eligible AS (
  SELECT
    outbox.run_id,
    outbox.workspace_id,
    outbox.issue_id,
    outbox.agent_id,
    outbox.issue_execution_revision,
    outbox.attempt_number,
    outbox.lease_token,
    agent.runtime_id,
    agent.model,
    agent.thinking_level
  FROM issue_dispatch_outbox outbox
  JOIN active_issue_execution claim
    ON claim.workspace_id = outbox.workspace_id
   AND claim.issue_id = outbox.issue_id
   AND claim.run_id = outbox.run_id
   AND claim.agent_id = outbox.agent_id
   AND claim.issue_execution_revision = outbox.issue_execution_revision
   AND claim.attempt_number = outbox.attempt_number
   AND claim.status = 'dispatching'
  JOIN issue issue_row
    ON issue_row.workspace_id = outbox.workspace_id
   AND issue_row.id = outbox.issue_id
   AND issue_row.execution_revision = outbox.issue_execution_revision
   AND issue_row.status IN ('todo', 'in_progress')
   AND issue_row.assignee_type = 'agent'
   AND issue_row.assignee_id = outbox.agent_id
  JOIN agent
    ON agent.workspace_id = outbox.workspace_id
   AND agent.id = outbox.agent_id
   AND agent.archived_at IS NULL
   AND agent.runtime_id IS NOT NULL
  WHERE outbox.id = @outbox_id
    AND outbox.workspace_id = @workspace_id
    AND outbox.status = 'delivering'
    AND outbox.lease_token = @lease_token
    AND NOT EXISTS (
      SELECT 1
      FROM issue_dependency dependency
      JOIN issue upstream ON upstream.id = dependency.depends_on_issue_id
      WHERE dependency.issue_id = outbox.issue_id
        AND dependency.type = 'blocked_by'
        AND upstream.status NOT IN ('done', 'cancelled')
    )
), inserted AS (
  INSERT INTO agent_inbox_event (
    id, workspace_id, agent_session_id, agent_id, runtime_id, execution_config,
    issue_id, reason, requires_wake, status, priority, force_fresh_session,
    context, parent_task_id, attempt, max_attempts,
    issue_run_kind, issue_execution_revision,
    issue_execution_attempt_number
  )
  SELECT
    eligible.run_id,
    eligible.workspace_id,
    ensure_agent_wake_session(eligible.agent_id),
    eligible.agent_id,
    eligible.runtime_id,
    @task_context,
    eligible.issue_id,
    'issue',
    true,
    'pending',
    @priority,
    @force_fresh_session,
    @task_context,
    sqlc.narg('parent_run_id'),
    CASE WHEN @delivery_attempt::integer > 0 THEN @delivery_attempt::integer ELSE 1 END,
    CASE WHEN @max_attempts::integer > 0 THEN @max_attempts::integer ELSE 3 END,
    'canonical',
    eligible.issue_execution_revision,
    eligible.attempt_number
  FROM eligible
  ON CONFLICT (id) DO NOTHING
  RETURNING *
)
SELECT * FROM inserted
UNION ALL
SELECT event.*
FROM agent_inbox_event event
JOIN eligible
  ON event.id = eligible.run_id
 AND event.workspace_id = eligible.workspace_id
 AND event.issue_id = eligible.issue_id
 AND event.agent_id = eligible.agent_id
 AND event.issue_run_kind = 'canonical'
 AND event.issue_execution_revision = eligible.issue_execution_revision
 AND event.issue_execution_attempt_number = eligible.attempt_number
LIMIT 1;

-- name: ClaimIssueDispatchOutboxByRun :one
UPDATE issue_dispatch_outbox
SET status = 'delivering',
    delivery_attempts = delivery_attempts + 1,
    lease_token = @lease_token,
    lease_expires_at = @lease_expires_at,
    last_error = '',
    updated_at = now()
WHERE workspace_id = @workspace_id
  AND run_id = @run_id
  AND (
    (status = 'pending' AND next_delivery_at <= now())
    OR (status = 'delivering' AND lease_expires_at <= now())
  )
RETURNING *;

-- name: ClaimIssueDispatchOutboxGlobal :many
WITH candidates AS (
  SELECT candidate.id
  FROM issue_dispatch_outbox AS candidate
  WHERE (candidate.status = 'pending' AND candidate.next_delivery_at <= now())
     OR (candidate.status = 'delivering' AND candidate.lease_expires_at <= now())
  ORDER BY candidate.next_delivery_at ASC, candidate.created_at ASC, candidate.id ASC
  FOR UPDATE SKIP LOCKED
  LIMIT @claim_limit
)
UPDATE issue_dispatch_outbox AS outbox
SET status = 'delivering',
    delivery_attempts = outbox.delivery_attempts + 1,
    lease_token = @lease_token,
    lease_expires_at = @lease_expires_at,
    last_error = '',
    updated_at = now()
FROM candidates
WHERE outbox.id = candidates.id
RETURNING outbox.*;

-- name: ListRunnableIssuesMissingExecution :many
SELECT issue_row.*
FROM issue issue_row
JOIN agent
  ON agent.workspace_id = issue_row.workspace_id
 AND agent.id = issue_row.assignee_id
 AND agent.archived_at IS NULL
 AND agent.runtime_id IS NOT NULL
WHERE issue_row.status IN ('todo', 'in_progress')
  AND issue_row.assignee_type = 'agent'
  AND issue_row.assignee_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM active_issue_execution claim WHERE claim.issue_id = issue_row.id
  )
  -- During rollout, do not replace an assignment wake that the legacy path
  -- already queued or started. Once that wake becomes terminal, the next
  -- recovery scan can establish the canonical claim without overlapping work.
  AND NOT EXISTS (
    SELECT 1
    FROM agent_inbox_event legacy_event
    WHERE legacy_event.workspace_id = issue_row.workspace_id
      AND legacy_event.issue_id = issue_row.id
      AND legacy_event.reason = 'issue'
      AND legacy_event.trigger_comment_id IS NULL
      AND legacy_event.issue_run_kind IS NULL
      AND legacy_event.status IN ('pending', 'failed', 'draining', 'running')
      AND legacy_event.terminal_outcome IS NULL
  )
  AND NOT EXISTS (
    SELECT 1
    FROM issue_dependency dependency
    JOIN issue upstream ON upstream.id = dependency.depends_on_issue_id
    WHERE dependency.issue_id = issue_row.id
      AND dependency.type = 'blocked_by'
      AND upstream.status NOT IN ('done', 'cancelled')
  )
ORDER BY issue_row.updated_at, issue_row.id
LIMIT @row_limit;
