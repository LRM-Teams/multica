-- Opt-in, correlation-scoped evidence for env-dispatch reclamation.
-- All report reads include the workspace and audit initiator so these queries
-- cannot become a workspace-wide daemon inventory.

-- name: CreateEnvDispatchAuditRun :one
INSERT INTO env_dispatch_audit_run (
    workspace_id,
    initiator_id,
    dispatch_type,
    primary_scope_id,
    reclamation_deadline,
    started_at
)
VALUES (
    @workspace_id,
    @initiator_id,
    @dispatch_type,
    @primary_scope_id,
    @reclamation_deadline,
    @started_at
)
RETURNING *;

-- name: GetEnvDispatchAuditRunForInitiator :one
SELECT *
FROM env_dispatch_audit_run
WHERE id = @audit_id
  AND workspace_id = @workspace_id
  AND initiator_id = @initiator_id;

-- name: UpdateEnvDispatchAuditRunOutcome :one
-- The first terminal outcome wins, so retries can safely repeat the write
-- without overwriting the outcome that the audit is meant to diagnose.
UPDATE env_dispatch_audit_run
SET outcome = CASE
        WHEN outcome = 'running' THEN @outcome
        ELSE outcome
    END,
    completed_at = CASE
        WHEN @completed_at::timestamptz IS NULL THEN completed_at
        WHEN completed_at IS NULL THEN @completed_at::timestamptz
        ELSE completed_at
    END,
    updated_at = now()
WHERE id = @audit_id
RETURNING *;

-- name: UpdateEnvDispatchAuditRunVerdict :one
-- Verdicts are terminal audit evidence: once classified, retries must not
-- silently revise a conclusive report.
UPDATE env_dispatch_audit_run
SET verdict = CASE
        WHEN verdict = 'pending' THEN @verdict
        ELSE verdict
    END,
    completed_at = CASE
        WHEN @completed_at::timestamptz IS NULL THEN completed_at
        WHEN completed_at IS NULL THEN @completed_at::timestamptz
        ELSE completed_at
    END,
    updated_at = now()
WHERE id = @audit_id
RETURNING *;

-- name: UpsertEnvDispatchAuditResource :one
-- Identity is immutable within an audit. Re-observation may fill in a missing
-- snapshot field, but it never downgrades shared ownership or a known owner
-- state back to unknown.
INSERT INTO env_dispatch_audit_resource (
    audit_id,
    resource_kind,
    resource_id,
    daemon_id,
    environment_id,
    project_id,
    channel_id,
    ownership_mode,
    owner_state,
    first_observed_at,
    last_observed_at
)
VALUES (
    @audit_id,
    @resource_kind,
    @resource_id,
    sqlc.narg(daemon_id),
    sqlc.narg(environment_id),
    sqlc.narg(project_id),
    sqlc.narg(channel_id),
    @ownership_mode,
    @owner_state,
    @observed_at,
    @observed_at
)
ON CONFLICT (audit_id, resource_kind, resource_id) DO UPDATE
SET daemon_id = COALESCE(EXCLUDED.daemon_id, env_dispatch_audit_resource.daemon_id),
    environment_id = COALESCE(EXCLUDED.environment_id, env_dispatch_audit_resource.environment_id),
    project_id = COALESCE(EXCLUDED.project_id, env_dispatch_audit_resource.project_id),
    channel_id = COALESCE(EXCLUDED.channel_id, env_dispatch_audit_resource.channel_id),
    ownership_mode = CASE
        WHEN env_dispatch_audit_resource.ownership_mode = 'shared'
          OR EXCLUDED.ownership_mode = 'shared' THEN 'shared'
        ELSE 'exclusive'
    END,
    owner_state = CASE
        WHEN EXCLUDED.owner_state = 'unknown' THEN env_dispatch_audit_resource.owner_state
        ELSE EXCLUDED.owner_state
    END,
    first_observed_at = LEAST(
        env_dispatch_audit_resource.first_observed_at,
        EXCLUDED.first_observed_at
    ),
    last_observed_at = GREATEST(
        COALESCE(
            env_dispatch_audit_resource.last_observed_at,
            env_dispatch_audit_resource.first_observed_at
        ),
        EXCLUDED.last_observed_at
    ),
    updated_at = now()
RETURNING *;

-- name: UpdateEnvDispatchAuditResourceClassification :one
UPDATE env_dispatch_audit_resource
SET owner_state = @owner_state,
    classification = @classification,
    last_observed_at = GREATEST(
        COALESCE(last_observed_at, first_observed_at),
        @observed_at
    ),
    reclaimed_at = CASE
        WHEN @classification = 'reclaimed' THEN COALESCE(reclaimed_at, @observed_at)
        ELSE reclaimed_at
    END,
    updated_at = now()
WHERE id = @audit_resource_id
  AND audit_id = @audit_id
RETURNING *;

-- name: ListEnvDispatchAuditResourcesForInitiator :many
SELECT resource.*
FROM env_dispatch_audit_resource AS resource
JOIN env_dispatch_audit_run AS audit ON audit.id = resource.audit_id
WHERE audit.id = @audit_id
  AND audit.workspace_id = @workspace_id
  AND audit.initiator_id = @initiator_id
ORDER BY resource.first_observed_at ASC, resource.id ASC;

-- name: EnsureEnvDispatchReclamationObligation :one
-- One resource has one current obligation. Repeating an enqueue after a crash
-- must preserve its state, retry count, and original trigger.
INSERT INTO env_dispatch_reclamation_obligation (
    audit_resource_id,
    trigger,
    state,
    next_attempt_at
)
VALUES (@audit_resource_id, @trigger, 'pending', @next_attempt_at)
ON CONFLICT (audit_resource_id) DO UPDATE
SET audit_resource_id = EXCLUDED.audit_resource_id
RETURNING *;

-- name: MarkEnvDispatchReclamationObligationSucceeded :one
UPDATE env_dispatch_reclamation_obligation
SET state = 'succeeded',
    next_attempt_at = NULL,
    last_error_code = NULL,
    updated_at = now()
WHERE id = @obligation_id
  AND state IN ('pending', 'in_progress', 'succeeded')
RETURNING *;

-- name: MarkEnvDispatchReclamationObligationNotRequired :one
UPDATE env_dispatch_reclamation_obligation
SET state = 'not_required',
    next_attempt_at = NULL,
    last_error_code = NULL,
    updated_at = now()
WHERE id = @obligation_id
  AND state IN ('pending', 'in_progress', 'not_required')
RETURNING *;

-- name: RescheduleEnvDispatchReclamationObligation :one
-- A claim increments attempt_count. Retry failures only move that same claim
-- back to pending; they never reset the bounded retry counter.
UPDATE env_dispatch_reclamation_obligation
SET state = 'pending',
    last_error_code = sqlc.narg(last_error_code),
    next_attempt_at = @next_attempt_at,
    updated_at = now()
WHERE id = @obligation_id
  AND state = 'in_progress'
RETURNING *;

-- name: ExhaustEnvDispatchReclamationObligation :one
UPDATE env_dispatch_reclamation_obligation
SET state = 'exhausted',
    last_error_code = sqlc.narg(last_error_code),
    next_attempt_at = NULL,
    updated_at = now()
WHERE id = @obligation_id
  AND state IN ('pending', 'in_progress', 'exhausted')
RETURNING *;

-- name: ListEnvDispatchReclamationObligationsForInitiator :many
SELECT obligation.*
FROM env_dispatch_reclamation_obligation AS obligation
JOIN env_dispatch_audit_resource AS resource
  ON resource.id = obligation.audit_resource_id
JOIN env_dispatch_audit_run AS audit ON audit.id = resource.audit_id
WHERE audit.id = @audit_id
  AND audit.workspace_id = @workspace_id
  AND audit.initiator_id = @initiator_id
ORDER BY resource.first_observed_at ASC, resource.id ASC;

-- name: ClaimEligibleEnvDispatchReclamationObligations :many
-- Claim in bounded, deterministic batches. SKIP LOCKED prevents concurrent
-- sweeps from issuing duplicate cleanup attempts; the deadline is never
-- extended, so expired obligations remain available for classification instead
-- of further retries.
WITH eligible AS (
    SELECT obligation.id
    FROM env_dispatch_reclamation_obligation AS obligation
    JOIN env_dispatch_audit_resource AS resource
      ON resource.id = obligation.audit_resource_id
    JOIN env_dispatch_audit_run AS audit ON audit.id = resource.audit_id
    WHERE obligation.state = 'pending'
      AND obligation.next_attempt_at <= @eligible_at
      AND audit.reclamation_deadline > @eligible_at
    ORDER BY obligation.next_attempt_at ASC, obligation.id ASC
    LIMIT @limit_count
    FOR UPDATE OF obligation SKIP LOCKED
), claimed AS (
    UPDATE env_dispatch_reclamation_obligation AS obligation
    SET state = 'in_progress',
        attempt_count = obligation.attempt_count + 1,
        next_attempt_at = NULL,
        updated_at = now()
    FROM eligible
    WHERE obligation.id = eligible.id
      AND obligation.state = 'pending'
    RETURNING obligation.*
)
SELECT
    claimed.id AS obligation_id,
    claimed.audit_resource_id,
    claimed.trigger,
    claimed.state,
    claimed.attempt_count,
    claimed.last_error_code,
    claimed.next_attempt_at,
    claimed.created_at AS obligation_created_at,
    claimed.updated_at AS obligation_updated_at,
    resource.audit_id,
    resource.resource_kind,
    resource.resource_id,
    resource.daemon_id,
    resource.environment_id,
    resource.project_id,
    resource.channel_id,
    resource.ownership_mode,
    resource.owner_state,
    resource.classification,
    audit.workspace_id,
    audit.initiator_id,
    audit.reclamation_deadline
FROM claimed
JOIN env_dispatch_audit_resource AS resource
  ON resource.id = claimed.audit_resource_id
JOIN env_dispatch_audit_run AS audit ON audit.id = resource.audit_id
ORDER BY claimed.created_at ASC, claimed.id ASC;

-- name: LockEnvDispatchAuditRunForEventAppend :one
-- Event writers must call this in the same transaction before determining the
-- next sequence and appending an event. The parent-row lock serializes the
-- per-audit sequence without exposing a mutable payload column.
SELECT *
FROM env_dispatch_audit_run
WHERE id = @audit_id
FOR UPDATE;

-- name: GetLastEnvDispatchAuditEventSequence :one
SELECT COALESCE(MAX(sequence), 0)::bigint AS sequence
FROM env_dispatch_audit_event
WHERE audit_id = @audit_id;

-- name: CreateEnvDispatchAuditEvent :one
-- Called after LockEnvDispatchAuditRunForEventAppend and
-- GetLastEnvDispatchAuditEventSequence in one transaction. The caller supplies
-- the next sequence, making retries explicit and preserving append-only events.
INSERT INTO env_dispatch_audit_event (
    audit_id,
    audit_resource_id,
    sequence,
    event_type,
    reason_code,
    occurred_at
)
VALUES (
    @audit_id,
    @audit_resource_id,
    @sequence,
    @event_type,
    sqlc.narg(reason_code),
    @occurred_at
)
RETURNING *;

-- name: ListEnvDispatchAuditEventsForInitiator :many
SELECT event.*
FROM env_dispatch_audit_event AS event
JOIN env_dispatch_audit_run AS audit ON audit.id = event.audit_id
WHERE audit.id = @audit_id
  AND audit.workspace_id = @workspace_id
  AND audit.initiator_id = @initiator_id
ORDER BY event.sequence ASC;
