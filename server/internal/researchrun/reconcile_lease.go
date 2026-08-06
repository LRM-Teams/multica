package researchrun

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrRunLeaseLost = errors.New("research run reconcile lease lost")

// RunLease is the database fencing identity for one reconcile execution.
// Generation is incremented for every fresh claim and never reused.
type RunLease struct {
	SessionID  string
	Token      string
	Generation int64
	ExpiresAt  time.Time
}

type runLeaseContextKey struct{}

func withRunLease(ctx context.Context, lease RunLease) context.Context {
	return context.WithValue(ctx, runLeaseContextKey{}, lease)
}

func runLeaseFromContext(ctx context.Context) (RunLease, bool) {
	lease, ok := ctx.Value(runLeaseContextKey{}).(RunLease)
	return lease, ok
}

// ReconcileLeaseFromContext is exposed for projection adapters that perform
// side effects outside the canonical Research Run store transaction.
func ReconcileLeaseFromContext(ctx context.Context) (RunLease, bool) {
	return runLeaseFromContext(ctx)
}

func (s *PostgresStore) AssertRunLease(ctx context.Context, sessionID string) error {
	lease, ok := runLeaseFromContext(ctx)
	if !ok || lease.SessionID != sessionID {
		return ErrRunLeaseLost
	}
	var one int
	err := s.pool.QueryRow(ctx, `
		SELECT 1
		FROM research_session
		WHERE id = $1::uuid
		  AND reconcile_lease_token = $2::uuid
		  AND reconcile_lease_generation = $3
		  AND reconcile_lease_expires_at > now()
	`, sessionID, lease.Token, lease.Generation).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRunLeaseLost
	}
	return err
}

// lockRunForMutation serializes a canonical mutation with reconcile claims.
// Reconcile-owned calls must prove their unexpired token and generation.
// External canonical mutations revoke an active reconciler before writing.
func lockRunForMutation(ctx context.Context, tx pgx.Tx, sessionID, workspaceID string) error {
	lease, fenced := runLeaseFromContext(ctx)
	if fenced {
		if lease.SessionID != sessionID {
			return ErrRunLeaseLost
		}
		query := `
			SELECT 1
			FROM research_session
			WHERE id = $1::uuid
			  AND reconcile_lease_token = $2::uuid
			  AND reconcile_lease_generation = $3
			  AND reconcile_lease_expires_at > now()`
		args := []any{sessionID, lease.Token, lease.Generation}
		if workspaceID != "" {
			query += ` AND workspace_id = $4::uuid`
			args = append(args, workspaceID)
		}
		query += ` FOR UPDATE`
		var one int
		if err := tx.QueryRow(ctx, query, args...).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRunLeaseLost
			}
			return err
		}
		return nil
	}

	query := `SELECT 1 FROM research_session WHERE id = $1::uuid`
	args := []any{sessionID}
	if workspaceID != "" {
		query += ` AND workspace_id = $2::uuid`
		args = append(args, workspaceID)
	}
	query += ` FOR UPDATE`
	var one int
	if err := tx.QueryRow(ctx, query, args...).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRunNotFound
		}
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE research_session
		SET reconcile_lease_token = NULL,
		    reconcile_lease_expires_at = NULL,
		    updated_at = CASE
		      WHEN reconcile_lease_token IS NULL THEN updated_at
		      ELSE now()
		    END
		WHERE id = $1::uuid
	`, sessionID)
	return err
}
