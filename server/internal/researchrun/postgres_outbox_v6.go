package researchrun

import (
	"context"
	"encoding/json"
	"time"
)

func (s *PostgresStore) ClaimV6Outbox(ctx context.Context, token string, lease time.Duration, limit int) ([]V6OutboxIntent, error) {
	rows, err := s.pool.Query(ctx, `WITH due AS (
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
	command, err := s.pool.Exec(ctx, `UPDATE research_v6_outbox
		SET status='pending',last_error=$3,next_delivery_at=$4,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
		WHERE id=$1::uuid AND lease_token=$2::uuid AND status='delivering'`, id, token, message, next)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrWorkItemLeaseLost
	}
	return nil
}
