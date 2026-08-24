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
