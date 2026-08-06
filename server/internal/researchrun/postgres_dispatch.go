package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ClaimDispatchIntents(ctx context.Context, sessionID, token string, lease time.Duration, limit int) ([]DispatchIntent, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT outbox.id
			FROM research_dispatch_outbox outbox
			JOIN research_session session ON session.id = outbox.session_id
			WHERE outbox.session_id = $1::uuid
			  AND session.status = 'running'
			  AND (
			    (outbox.status = 'pending' AND outbox.next_delivery_at <= now())
			    OR (outbox.status = 'delivering' AND outbox.lease_expires_at < now())
			  )
			ORDER BY outbox.next_delivery_at, outbox.created_at, outbox.id
			FOR UPDATE OF outbox SKIP LOCKED
			LIMIT $4
		)
		UPDATE research_dispatch_outbox outbox
		SET status = 'delivering', lease_token = $2::uuid,
		    lease_expires_at = now() + $3::interval,
		    delivery_attempts = outbox.delivery_attempts + 1,
		    updated_at = now()
		FROM candidates
		WHERE outbox.id = candidates.id
		RETURNING outbox.id::text, outbox.attempt_id::text,
		          outbox.session_id::text, outbox.request_payload,
		          outbox.delivery_attempts
	`, sessionID, token, lease.String(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intents := []DispatchIntent{}
	for rows.Next() {
		var intent DispatchIntent
		var payload []byte
		if err = rows.Scan(&intent.ID, &intent.AttemptID, &intent.SessionID, &payload, &intent.DeliveryAttempts); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(payload, &intent.Request); err != nil {
			return nil, fmt.Errorf("decode dispatch intent %s: %w", intent.ID, err)
		}
		hash, hashErr := HashDispatchRequest(intent.Request)
		if hashErr != nil || hash != intent.Request.RequestHash {
			return nil, fmt.Errorf("%w: dispatch intent %s request hash mismatch", ErrResultConflict, intent.ID)
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func (s *PostgresStore) RescheduleDispatchIntent(ctx context.Context, intentID, token, diagnostics string, next time.Time) (bool, error) {
	command, err := s.pool.Exec(ctx, `
		UPDATE research_dispatch_outbox
		SET status = 'pending', lease_token = NULL, lease_expires_at = NULL,
		    next_delivery_at = $3, last_error = $4, updated_at = now()
		WHERE id = $1::uuid AND status = 'delivering' AND lease_token = $2::uuid
	`, intentID, token, next, truncateBytes(diagnostics, 4096))
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (s *PostgresStore) FailDispatchIntent(ctx context.Context, intentID, token string, failure AttemptFailure) (bool, RunEvent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	var sessionID string
	if err = tx.QueryRow(ctx, `SELECT session_id::text FROM research_dispatch_outbox WHERE id = $1::uuid`, intentID).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
		return false, RunEvent{}, ErrRunNotFound
	} else if err != nil {
		return false, RunEvent{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT 1 FROM research_session WHERE id = $1::uuid FOR UPDATE`, sessionID); err != nil {
		return false, RunEvent{}, err
	}
	var attemptID, status, leaseToken string
	err = tx.QueryRow(ctx, `
		SELECT attempt_id::text, status, COALESCE(lease_token::text, '')
		FROM research_dispatch_outbox
		WHERE id = $1::uuid
		FOR UPDATE
	`, intentID).Scan(&attemptID, &status, &leaseToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, RunEvent{}, ErrRunNotFound
	}
	if err != nil {
		return false, RunEvent{}, err
	}
	if status == "cancelled" || status == "failed" {
		return false, RunEvent{}, tx.Commit(ctx)
	}
	if status != "delivering" || leaseToken != token || failure.AttemptID != attemptID {
		return false, RunEvent{}, fmt.Errorf("%w: dispatch claim changed", ErrInvalidTransition)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_dispatch_outbox
		SET status = 'failed', lease_token = NULL, lease_expires_at = NULL,
		    last_error = $2, updated_at = now()
		WHERE id = $1::uuid
	`, intentID, truncateBytes(failure.Diagnostics, 4096)); err != nil {
		return false, RunEvent{}, err
	}
	event, err := failAttemptTx(ctx, tx, failure)
	if errors.Is(err, ErrInvalidTransition) {
		return false, RunEvent{}, tx.Commit(ctx)
	}
	if err != nil {
		return false, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, RunEvent{}, err
	}
	return true, event, nil
}

// AcknowledgeDispatchIntent atomically records the external Inbox identity and
// advances Attempt/Task state. accepted=false means a concurrent control action
// made the Attempt terminal; the caller must cancel the external task.
func (s *PostgresStore) AcknowledgeDispatchIntent(ctx context.Context, intentID, token, inboxTaskID string) (accepted bool, attempt Attempt, event RunEvent, retErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, Attempt{}, RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	var sessionID string
	if err = tx.QueryRow(ctx, `SELECT session_id::text FROM research_dispatch_outbox WHERE id = $1::uuid`, intentID).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
		return false, Attempt{}, RunEvent{}, ErrRunNotFound
	} else if err != nil {
		return false, Attempt{}, RunEvent{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT 1 FROM research_session WHERE id = $1::uuid FOR UPDATE`, sessionID); err != nil {
		return false, Attempt{}, RunEvent{}, err
	}
	var outboxStatus, leaseToken, attemptStatus string
	err = tx.QueryRow(ctx, `
		SELECT outbox.status, COALESCE(outbox.lease_token::text, ''), attempt.status
		FROM research_dispatch_outbox outbox
		JOIN research_task_attempt attempt ON attempt.id = outbox.attempt_id
		WHERE outbox.id = $1::uuid
		FOR UPDATE OF outbox, attempt
	`, intentID).Scan(&outboxStatus, &leaseToken, &attemptStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, Attempt{}, RunEvent{}, ErrRunNotFound
	}
	if err != nil {
		return false, Attempt{}, RunEvent{}, err
	}
	if outboxStatus == "cancelled" || attemptStatus != string(AttemptStatusDispatching) {
		return false, Attempt{}, RunEvent{}, tx.Commit(ctx)
	}
	if outboxStatus != "delivering" || leaseToken != token {
		return false, Attempt{}, RunEvent{}, fmt.Errorf("%w: dispatch claim changed", ErrInvalidTransition)
	}
	err = tx.QueryRow(ctx, `
		UPDATE research_task_attempt
		SET inbox_task_id = $2::uuid, updated_at = now()
		WHERE id = (SELECT attempt_id FROM research_dispatch_outbox WHERE id = $1::uuid)
		  AND status = 'dispatching'
		RETURNING id::text, session_id::text, workspace_id::text, task_id::text,
		          attempt_number, assigned_agent_id::text, inbox_task_id::text,
		          dispatch_key, COALESCE(client_request_id, ''), status,
		          COALESCE(result_hash, ''), failure_class, diagnostics, dispatched_at,
		          started_at, runtime_started_at, runtime_last_observed_at,
		          runtime_lease_expires_at, cancellation_requested_at,
		          cancellation_completed_at, pending_failure_class,
		          pending_failure_diagnostics, pending_failure_retryable,
		          result_submitted_at, completed_at
	`, intentID, inboxTaskID).Scan(&attempt.ID, &attempt.SessionID, &attempt.WorkspaceID,
		&attempt.TaskID, &attempt.AttemptNumber, &attempt.AssignedAgentID,
		&attempt.InboxTaskID, &attempt.DispatchKey, &attempt.ClientRequestID,
		&attempt.Status, &attempt.ResultHash, &attempt.FailureClass,
		&attempt.Diagnostics, &attempt.DispatchedAt, &attempt.StartedAt,
		&attempt.RuntimeStartedAt, &attempt.RuntimeObservedAt, &attempt.RuntimeLeaseUntil,
		&attempt.CancelRequestedAt, &attempt.CancelCompletedAt, &attempt.PendingFailure,
		&attempt.PendingDiagnostics, &attempt.PendingRetryable,
		&attempt.ResultSubmittedAt, &attempt.CompletedAt)
	if err != nil {
		return false, Attempt{}, RunEvent{}, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_dispatch_outbox
		SET status = 'delivered', delivered_at = now(), lease_token = NULL,
		    lease_expires_at = NULL, last_error = '', updated_at = now()
		WHERE id = $1::uuid
	`, intentID); err != nil {
		return false, Attempt{}, RunEvent{}, err
	}
	event, err = appendEvent(ctx, tx, attempt.WorkspaceID, attempt.SessionID, "task_dispatched", "task-dispatched:"+attempt.ID, "system", "", map[string]any{
		"task_id": attempt.TaskID, "attempt_id": attempt.ID, "inbox_task_id": inboxTaskID, "agent_id": attempt.AssignedAgentID,
	})
	if err != nil {
		return false, Attempt{}, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, Attempt{}, RunEvent{}, err
	}
	return true, attempt, event, nil
}
