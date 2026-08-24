package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ClaimV6Outbox(ctx context.Context, token string, lease time.Duration, limit int) ([]V6OutboxIntent, error) {
	rows, err := s.pool.Query(ctx, `WITH stale_dispatch AS (
		UPDATE research_v6_outbox o
		SET status='failed', lease_token=NULL, lease_expires_at=NULL,
			last_error=CASE WHEN last_error='' THEN 'stale dispatch attempt' ELSE last_error END,
			updated_at=now()
		WHERE o.kind='dispatch_work_item' AND o.status IN ('pending','delivering')
		  AND (o.lease_expires_at IS NULL OR o.lease_expires_at <= now())
		  AND NOT EXISTS (
			SELECT 1 FROM research_work_item_attempt a
			WHERE a.id::text=COALESCE(o.payload->'access'->>'attempt_id',o.payload->'access'->>'AttemptID')
			  AND a.status='dispatching'
		  )
		RETURNING o.id
	), due AS (
		SELECT id FROM research_v6_outbox
		WHERE status IN ('pending','delivering') AND next_delivery_at <= now()
		  AND (lease_expires_at IS NULL OR lease_expires_at <= now())
		ORDER BY next_delivery_at,id FOR UPDATE SKIP LOCKED LIMIT $1
	) UPDATE research_v6_outbox o
	SET status='delivering', lease_token=$2::uuid, lease_expires_at=now()+$3::interval,
		delivery_attempts=delivery_attempts+1, updated_at=now()
	WHERE o.id IN (SELECT id FROM due)
	RETURNING id::text,workspace_id::text,session_id::text,kind,idempotency_key,payload,delivery_attempts`, limit, token, lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []V6OutboxIntent{}
	for rows.Next() {
		var item V6OutboxIntent
		if err = rows.Scan(&item.ID, &item.WorkspaceID, &item.RunID, &item.Kind, &item.IdempotencyKey, &item.Payload, &item.DeliveryAttempts); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CompleteV6Outbox(ctx context.Context, id, token string, result json.RawMessage) error {
	command, err := s.pool.Exec(ctx, `UPDATE research_v6_outbox
		SET status='delivered',result=$3::jsonb,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE id=$1::uuid AND lease_token=$2::uuid AND status='delivering'`, id, token, result)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrWorkItemLeaseLost
	}
	return nil
}

func (s *PostgresStore) RescheduleV6Outbox(ctx context.Context, id, token, message string, next time.Time) error {
	tx, err := s.beginResearchTx(ctx, txOpV6OutboxReschedule, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var workspaceID, runID, kind, idempotencyKey, status string
	err = tx.QueryRow(ctx, `UPDATE research_v6_outbox
		SET status=CASE WHEN delivery_attempts>=20 THEN 'failed' ELSE 'pending' END,
			last_error=$3,next_delivery_at=$4,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE id=$1::uuid AND lease_token=$2::uuid AND status='delivering'
		RETURNING workspace_id::text,session_id::text,kind,idempotency_key,status`, id, token, message, next).Scan(
		&workspaceID, &runID, &kind, &idempotencyKey, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWorkItemLeaseLost
	}
	if err != nil {
		return err
	}
	if status == "failed" {
		if err = appendV6OutboxFailureEvent(ctx, tx, workspaceID, runID, id, kind, idempotencyKey, message); err != nil {
			return err
		}
	}
	return s.commitResearchTx(ctx, txOpV6OutboxReschedule, tx)
}

// FailV6Outbox terminates an intent whose delivery can never succeed
// (contract-invalid payload, non-retryable adapter classification) without
// burning the remaining retry budget. It emits the same run event as retry
// exhaustion so the failure is visible outside the outbox table.
func (s *PostgresStore) FailV6Outbox(ctx context.Context, id, token, message string) error {
	tx, err := s.beginResearchTx(ctx, txOpV6OutboxFail, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var workspaceID, runID, kind, idempotencyKey string
	err = tx.QueryRow(ctx, `UPDATE research_v6_outbox
		SET status='failed',last_error=$3,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE id=$1::uuid AND lease_token=$2::uuid AND status='delivering'
		RETURNING workspace_id::text,session_id::text,kind,idempotency_key`, id, token, message).Scan(
		&workspaceID, &runID, &kind, &idempotencyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWorkItemLeaseLost
	}
	if err != nil {
		return err
	}
	if err = appendV6OutboxFailureEvent(ctx, tx, workspaceID, runID, id, kind, idempotencyKey, message); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6OutboxFail, tx)
}

func appendV6OutboxFailureEvent(ctx context.Context, tx pgx.Tx, workspaceID, runID, outboxID, kind, idempotencyKey, message string) error {
	_, err := appendEvent(ctx, tx, workspaceID, runID, "v6_outbox_delivery_failed", "v6-outbox-failed:"+outboxID,
		"system", "", map[string]any{"outbox_id": outboxID, "kind": kind, "idempotency_key": idempotencyKey, "error": message})
	return err
}
