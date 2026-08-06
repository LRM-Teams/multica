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

// updateIntentBaseRetryBackoff/updateIntentMaxRetryBackoff/
// updateIntentMaxConsecutiveFailures bound how hard a persistently-failing
// (reachable but broken — not asleep) runtime gets hammered. Without this, a
// runtime that heartbeats every ~15s (DefaultHeartbeatInterval) and fails
// every single attempt would get a freshly materialized attempt on nearly
// every heartbeat for up to 14 days — tens of thousands of attempts against
// one machine (Parker's review catch, 2026-08-02, using 群管理/MAOZH2's
// Windows stage-commit rename failure as the concrete case: every attempt
// against it fails, so without backoff this exact machine would get hammered
// the hardest). Exponential backoff starting at 1 minute, doubling, capped at
// 6h — the 6h cap deliberately matches autoUpdateLoop's own
// DefaultAutoUpdateCheckInterval, an existing "don't check more often than
// this" convention in this codebase. After
// updateIntentMaxConsecutiveFailures (8, chosen to allow ~4h of real-time
// backed-off retries — long enough to rule out a genuinely transient failure
// like a few-second AV scan, short enough not to hammer a truly broken
// machine for 14 days straight) the intent stops auto-retrying and is marked
// GivenUpAt — visible, not silently abandoned.
const (
	updateIntentBaseRetryBackoff       = time.Minute
	updateIntentMaxRetryBackoff        = 6 * time.Hour
	updateIntentMaxConsecutiveFailures = 8
)

// updateIntentRetryBackoff returns how long to wait before the next
// materialization attempt after consecutiveFailures in a row. 0 failures
// means "eligible now".
func updateIntentRetryBackoff(consecutiveFailures int) time.Duration {
	if consecutiveFailures <= 0 {
		return 0
	}
	shift := consecutiveFailures - 1
	if shift > 62 { // guard against overflow before the cap comparison below
		return updateIntentMaxRetryBackoff
	}
	backoff := updateIntentBaseRetryBackoff * time.Duration(int64(1)<<uint(shift))
	if backoff > updateIntentMaxRetryBackoff || backoff <= 0 {
		return updateIntentMaxRetryBackoff
	}
	return backoff
}

// UpdateIntent is a durable "this runtime should be updated" record, separate
// from any single delivery attempt (UpdateRequest / daemon_runtime_update).
// It has no target version: it always resolves to whatever
// RuntimeReleaseSource reports as latest at the moment it's materialized into
// an attempt, not whatever was newest when the intent was created — an
// intent that outlives its target by days must not install a version we've
// since found and fixed a bug in.
type UpdateIntent struct {
	RuntimeID           string
	CreatedBy           string
	CreatedAt           time.Time
	ExpiresAt           time.Time
	CancelledAt         *time.Time
	ExpiredAt           *time.Time
	ConsecutiveFailures int
	NextRetryAt         time.Time
	LastFailedAttemptID string
	GivenUpAt           *time.Time
}

// Live reports whether the intent is still eligible to be materialized into
// an attempt — not cancelled, not expired, and hasn't given up after
// repeated failures.
func (i *UpdateIntent) Live() bool {
	return i != nil && i.CancelledAt == nil && i.ExpiredAt == nil && i.GivenUpAt == nil
}

type UpdateIntentStore interface {
	// Create replaces any existing intent for runtimeID (last-write-wins) so
	// historical rows can be repaired without inheriting stale backoff state.
	// Resets any prior failure/backoff bookkeeping — a fresh request deserves
	// an immediate attempt, not to inherit a previous request's penalty box.
	Create(ctx context.Context, runtimeID, createdBy string, ttl time.Duration) (*UpdateIntent, error)
	// Get returns the current intent for runtimeID, or nil if none exists.
	Get(ctx context.Context, runtimeID string) (*UpdateIntent, error)
	// Cancel marks a live intent cancelled. No-op if none exists or it's
	// already cancelled/expired/given-up.
	Cancel(ctx context.Context, runtimeID string) error
	// MarkExpired marks a live intent expired. Never deletes the row — an
	// expired intent stays visible (not silently gone) until superseded by a
	// fresh Create or explicitly cancelled.
	MarkExpired(ctx context.Context, runtimeID string) error
	// RecordFailure folds a newly observed failed/timed-out attempt into the
	// intent's backoff state: increments ConsecutiveFailures, advances
	// NextRetryAt by updateIntentRetryBackoff, and — once
	// updateIntentMaxConsecutiveFailures is reached — sets GivenUpAt so
	// materialization stops. Idempotent per attemptID: calling it again with
	// the same attemptID (e.g. from a heartbeat that arrives before
	// NextRetryAt) is a no-op, since last_failed_attempt_id already matches.
	RecordFailure(ctx context.Context, runtimeID, attemptID string) error
	// IsDueForRetry reports whether a live intent's backoff window has
	// elapsed. The comparison (next_retry_at <= now()) runs entirely in SQL
	// rather than fetching NextRetryAt and comparing against Go's
	// time.Now() — task #80: NextRetryAt is written by the database's own
	// clock (Create/RecordFailure both use now()), so comparing it against
	// the application server's clock introduced a window (observed under
	// DB connection-pool pressure in tests) where a just-created or
	// just-retried intent could be misjudged as "not due yet" until the
	// next heartbeat. Returns false (not an error) if no intent exists.
	IsDueForRetry(ctx context.Context, runtimeID string) (bool, error)
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
			expired_at = NULL,
			consecutive_failures = 0,
			next_retry_at = now(),
			last_failed_attempt_id = NULL,
			given_up_at = NULL
		RETURNING
			runtime_id::text, created_by::text, created_at, expires_at, cancelled_at, expired_at,
			consecutive_failures, next_retry_at, COALESCE(last_failed_attempt_id, ''), given_up_at
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
		SELECT
			runtime_id::text, created_by::text, created_at, expires_at, cancelled_at, expired_at,
			consecutive_failures, next_retry_at, COALESCE(last_failed_attempt_id, ''), given_up_at
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
		WHERE runtime_id = $1 AND cancelled_at IS NULL AND expired_at IS NULL AND given_up_at IS NULL
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
		WHERE runtime_id = $1 AND cancelled_at IS NULL AND expired_at IS NULL AND given_up_at IS NULL AND expires_at < now()
	`, runtimeID); err != nil {
		return fmt.Errorf("mark daemon runtime update intent expired: %w", err)
	}
	return nil
}

// RecordFailure computes the new consecutive-failure count in Go but leaves
// NextRetryAt's actual value to SQL (now() + interval) — task #80: this
// column must always be written using the database's own clock, matching
// Create()'s `next_retry_at = now()`, so IsDueForRetry's later `<= now()`
// comparison never crosses between the application server's clock and the
// database's. Only the backoff *duration* (updateIntentRetryBackoff — the
// single source of truth also used anywhere else backoff needs
// explaining/testing) is Go-computed; the absolute timestamp is not.
// given_up_at is likewise computed with SQL's now() via a CASE, guarded by
// last_failed_attempt_id so a heartbeat that observes the same terminal
// attempt more than once (e.g. arriving again before NextRetryAt) doesn't
// double-count it.
func (s *PostgresUpdateIntentStore) RecordFailure(ctx context.Context, runtimeID, attemptID string) error {
	if err := s.requireDB(); err != nil {
		return err
	}
	intent, err := s.Get(ctx, runtimeID)
	if err != nil {
		return err
	}
	if !intent.Live() || intent.LastFailedAttemptID == attemptID {
		return nil // already recorded, or no longer live — nothing to do
	}
	newCount := intent.ConsecutiveFailures + 1
	backoffSeconds := updateIntentRetryBackoff(newCount).Seconds()
	givenUp := newCount >= updateIntentMaxConsecutiveFailures
	if _, err := s.db.Exec(ctx, `
		UPDATE daemon_runtime_update_intent
		SET
			consecutive_failures = $2,
			last_failed_attempt_id = $3,
			next_retry_at = now() + ($4 * interval '1 second'),
			given_up_at = CASE WHEN $5 THEN now() ELSE NULL END
		WHERE runtime_id = $1
		  AND cancelled_at IS NULL AND expired_at IS NULL AND given_up_at IS NULL
		  AND last_failed_attempt_id IS DISTINCT FROM $3
	`, runtimeID, newCount, attemptID, backoffSeconds, givenUp); err != nil {
		return fmt.Errorf("record daemon runtime update intent failure: %w", err)
	}
	return nil
}

// IsDueForRetry reports whether a live intent's backoff window has elapsed,
// comparing next_retry_at against now() entirely within SQL. See the
// UpdateIntentStore interface doc for why this exists instead of fetching
// NextRetryAt and comparing it in Go.
func (s *PostgresUpdateIntentStore) IsDueForRetry(ctx context.Context, runtimeID string) (bool, error) {
	if err := s.requireDB(); err != nil {
		return false, err
	}
	var due bool
	err := s.db.QueryRow(ctx, `
		SELECT next_retry_at <= now()
		FROM daemon_runtime_update_intent
		WHERE runtime_id = $1
	`, runtimeID).Scan(&due)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check daemon runtime update intent retry due: %w", err)
	}
	return due, nil
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
	var cancelledAt, expiredAt, givenUpAt *time.Time
	err := row.Scan(
		&intent.RuntimeID,
		&intent.CreatedBy,
		&intent.CreatedAt,
		&intent.ExpiresAt,
		&cancelledAt,
		&expiredAt,
		&intent.ConsecutiveFailures,
		&intent.NextRetryAt,
		&intent.LastFailedAttemptID,
		&givenUpAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	intent.CancelledAt = cancelledAt
	intent.ExpiredAt = expiredAt
	intent.GivenUpAt = givenUpAt
	return &intent, nil
}

var _ UpdateIntentStore = (*PostgresUpdateIntentStore)(nil)
