package handler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UpdateIntentTTL is how long an unfulfilled intent survives before it's
// marked expired. Deliberately generous (14 days, not tuned from load data)
// so a laptop closed over a weekend or vacation still has a live intent
// waiting for it — this replaces the 120-second daemon_runtime_update pending
// window that caused the 2026-08-01 incident, not a precisely derived number.
// See docs/superpowers/specs/2026-08-01-daemon-update-mechanism-design.md.
const UpdateIntentTTL = 14 * 24 * time.Hour

// UpdateIntent is a durable "this runtime should be updated" record, separate
// from any single delivery attempt (UpdateRequest / daemon_runtime_update).
// It has no target version: it always resolves to whatever
// RuntimeReleaseSource reports as latest at the moment it's materialized into
// an attempt, not whatever was newest when the intent was created — an
// intent that outlives its target by days must not install a version we've
// since found and fixed a bug in.
type UpdateIntent struct {
	RuntimeID   string
	CreatedBy   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	CancelledAt *time.Time
	ExpiredAt   *time.Time
}

// Live reports whether the intent is still eligible to be materialized into
// an attempt — not cancelled, not yet marked expired.
func (i *UpdateIntent) Live() bool {
	return i != nil && i.CancelledAt == nil && i.ExpiredAt == nil
}

type UpdateIntentStore interface {
	// Create replaces any existing intent for runtimeID (last-write-wins —
	// mirrors InitiateUpdate's existing single-active-request semantics).
	Create(ctx context.Context, runtimeID, createdBy string, ttl time.Duration) (*UpdateIntent, error)
	// Get returns the current intent for runtimeID, or nil if none exists.
	Get(ctx context.Context, runtimeID string) (*UpdateIntent, error)
	// Cancel marks a live intent cancelled. No-op if none exists or it's
	// already cancelled/expired.
	Cancel(ctx context.Context, runtimeID string) error
	// MarkExpired marks a live intent expired. Never deletes the row — an
	// expired intent stays visible (not silently gone) until superseded by a
	// fresh Create or explicitly cancelled.
	MarkExpired(ctx context.Context, runtimeID string) error
	// Delete removes the intent row entirely. Only called once its
	// materialized attempt reaches UpdateCompleted — a fulfilled intent has
	// nothing left to track.
	Delete(ctx context.Context, runtimeID string) error
}

type PostgresUpdateIntentStore struct {
	db updatePostgresDB
}

func NewPostgresUpdateIntentStore(db updatePostgresDB) *PostgresUpdateIntentStore {
	return &PostgresUpdateIntentStore{db: db}
}

func (s *PostgresUpdateIntentStore) requireDB() error {
	if s == nil || s.db == nil {
		return errors.New("daemon runtime update intent PostgreSQL store is not configured")
	}
	return nil
}

func (s *PostgresUpdateIntentStore) Create(ctx context.Context, runtimeID, createdBy string, ttl time.Duration) (*UpdateIntent, error) {
	if err := s.requireDB(); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = UpdateIntentTTL
	}
	intent, err := scanUpdateIntent(s.db.QueryRow(ctx, `
		INSERT INTO daemon_runtime_update_intent (runtime_id, created_by, expires_at)
		VALUES ($1, $2, now() + ($3 * interval '1 second'))
		ON CONFLICT (runtime_id) DO UPDATE SET
			created_by = EXCLUDED.created_by,
			created_at = now(),
			expires_at = EXCLUDED.expires_at,
			cancelled_at = NULL,
			expired_at = NULL
		RETURNING runtime_id::text, created_by::text, created_at, expires_at, cancelled_at, expired_at
	`, runtimeID, createdBy, ttl.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("create daemon runtime update intent: %w", err)
	}
	return intent, nil
}

func (s *PostgresUpdateIntentStore) Get(ctx context.Context, runtimeID string) (*UpdateIntent, error) {
	if err := s.requireDB(); err != nil {
		return nil, err
	}
	intent, err := scanUpdateIntent(s.db.QueryRow(ctx, `
		SELECT runtime_id::text, created_by::text, created_at, expires_at, cancelled_at, expired_at
		FROM daemon_runtime_update_intent
		WHERE runtime_id = $1
	`, runtimeID))
	if err != nil {
		return nil, fmt.Errorf("get daemon runtime update intent: %w", err)
	}
	return intent, nil
}

func (s *PostgresUpdateIntentStore) Cancel(ctx context.Context, runtimeID string) error {
	if err := s.requireDB(); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE daemon_runtime_update_intent
		SET cancelled_at = now()
		WHERE runtime_id = $1 AND cancelled_at IS NULL AND expired_at IS NULL
	`, runtimeID); err != nil {
		return fmt.Errorf("cancel daemon runtime update intent: %w", err)
	}
	return nil
}

func (s *PostgresUpdateIntentStore) MarkExpired(ctx context.Context, runtimeID string) error {
	if err := s.requireDB(); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE daemon_runtime_update_intent
		SET expired_at = now()
		WHERE runtime_id = $1 AND cancelled_at IS NULL AND expired_at IS NULL AND expires_at < now()
	`, runtimeID); err != nil {
		return fmt.Errorf("mark daemon runtime update intent expired: %w", err)
	}
	return nil
}

func (s *PostgresUpdateIntentStore) Delete(ctx context.Context, runtimeID string) error {
	if err := s.requireDB(); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `
		DELETE FROM daemon_runtime_update_intent WHERE runtime_id = $1
	`, runtimeID); err != nil {
		return fmt.Errorf("delete daemon runtime update intent: %w", err)
	}
	return nil
}

type updateIntentScanner interface {
	Scan(dest ...any) error
}

func scanUpdateIntent(row updateIntentScanner) (*UpdateIntent, error) {
	var intent UpdateIntent
	var cancelledAt, expiredAt *time.Time
	err := row.Scan(
		&intent.RuntimeID,
		&intent.CreatedBy,
		&intent.CreatedAt,
		&intent.ExpiresAt,
		&cancelledAt,
		&expiredAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	intent.CancelledAt = cancelledAt
	intent.ExpiredAt = expiredAt
	return &intent, nil
}

var _ UpdateIntentStore = (*PostgresUpdateIntentStore)(nil)
