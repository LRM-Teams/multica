package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const activeRuntimeUpdateConstraint = "daemon_runtime_update_one_active_per_runtime_idx"

// PostgresUpdateStore is the production source of truth for daemon CLI update
// requests. The lifecycle must survive API process replacement and cannot
// depend on an optional Redis deployment.
type PostgresUpdateStore struct {
	db updatePostgresDB
}

type updatePostgresDB interface {
	dbExecutor
	txStarter
}

func NewPostgresUpdateStore(db updatePostgresDB) *PostgresUpdateStore {
	return &PostgresUpdateStore{db: db}
}

func (s *PostgresUpdateStore) Create(ctx context.Context, runtimeID, targetVersion string) (*UpdateRequest, error) {
	return s.create(ctx, randomID(), runtimeID, targetVersion)
}

func (s *PostgresUpdateStore) create(ctx context.Context, id, runtimeID, targetVersion string) (*UpdateRequest, error) {
	if err := s.requireDB(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create daemon runtime update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.expireStale(ctx, tx, "runtime_id = $4", runtimeID); err != nil {
		return nil, err
	}
	if existing, err := scanPostgresUpdate(tx.QueryRow(ctx, postgresUpdateSelect+` WHERE id = $1`, id)); err != nil {
		return nil, fmt.Errorf("load daemon runtime update by ID: %w", err)
	} else if existing != nil {
		if existing.RuntimeID == runtimeID && existing.TargetVersion == targetVersion {
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("commit replay daemon runtime update: %w", err)
			}
			return existing, nil
		}
		return nil, &updateError{msg: "update ID is already bound to another runtime update"}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM daemon_runtime_update
		WHERE updated_at < now() - ($1 * interval '1 second')
		  AND status IN ('completed', 'failed', 'timeout')
	`, updateTerminalRetention.Seconds()); err != nil {
		return nil, fmt.Errorf("prune daemon runtime updates: %w", err)
	}

	req, err := scanPostgresUpdate(tx.QueryRow(ctx, `
		INSERT INTO daemon_runtime_update (
			id,
			runtime_id,
			status,
			target_version
		)
		VALUES ($1, $2, 'pending', $3)
		RETURNING
			id,
			runtime_id::text,
			status,
			target_version,
			output,
			error,
			run_started_at,
			created_at,
			updated_at
	`, id, runtimeID, targetVersion))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == activeRuntimeUpdateConstraint {
			return nil, errUpdateInProgress
		}
		return nil, fmt.Errorf("create daemon runtime update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create daemon runtime update: %w", err)
	}
	return req, nil
}

func (s *PostgresUpdateStore) Get(ctx context.Context, id string) (*UpdateRequest, error) {
	if err := s.requireDB(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin get daemon runtime update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.expireStale(ctx, tx, "id = $4", id); err != nil {
		return nil, err
	}
	req, err := scanPostgresUpdate(tx.QueryRow(ctx, postgresUpdateSelect+`
		WHERE id = $1
	`, id))
	if err != nil {
		return nil, fmt.Errorf("get daemon runtime update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit get daemon runtime update: %w", err)
	}
	return req, nil
}

func (s *PostgresUpdateStore) LatestForRuntime(ctx context.Context, runtimeID string) (*UpdateRequest, error) {
	if err := s.requireDB(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin latest daemon runtime update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.expireStale(ctx, tx, "runtime_id = $4", runtimeID); err != nil {
		return nil, err
	}
	req, err := scanPostgresUpdate(tx.QueryRow(ctx, postgresUpdateSelect+`
		WHERE runtime_id = $1
		ORDER BY updated_at DESC, created_at DESC, id DESC
		LIMIT 1
	`, runtimeID))
	if err != nil {
		return nil, fmt.Errorf("get latest daemon runtime update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit latest daemon runtime update: %w", err)
	}
	return req, nil
}

// LatestForRuntimes returns the latest request for every supplied runtime in
// one transaction. It preserves LatestForRuntime's stale-update transition
// before reading while avoiding one transaction per runtime-list row.
func (s *PostgresUpdateStore) LatestForRuntimes(ctx context.Context, runtimeIDs []string) (map[string]*UpdateRequest, error) {
	latest := make(map[string]*UpdateRequest)
	if len(runtimeIDs) == 0 {
		return latest, nil
	}
	if err := s.requireDB(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin latest daemon runtime updates: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.expireStaleForRuntimes(ctx, tx, runtimeIDs); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, postgresUpdateSelect+`
		WHERE runtime_id = ANY($1::text[]::uuid[])
		ORDER BY runtime_id, updated_at DESC, created_at DESC, id DESC
	`, runtimeIDs)
	if err != nil {
		return nil, fmt.Errorf("list latest daemon runtime updates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		req, err := scanPostgresUpdate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan latest daemon runtime update: %w", err)
		}
		if _, ok := latest[req.RuntimeID]; !ok {
			latest[req.RuntimeID] = req
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list latest daemon runtime updates: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit latest daemon runtime updates: %w", err)
	}
	return latest, nil
}

func (s *PostgresUpdateStore) expireStaleForRuntimes(ctx context.Context, exec dbExecutor, runtimeIDs []string) error {
	if _, err := exec.Exec(ctx, `
		UPDATE daemon_runtime_update
		SET
			status = 'timeout',
			error = CASE status
				WHEN 'pending' THEN 'daemon did not respond within 120 seconds'
				WHEN 'ready_to_apply' THEN 'activation did not complete within 20 minutes'
				ELSE 'update did not complete within 150 seconds'
			END,
			updated_at = now()
		WHERE runtime_id = ANY($4::text[]::uuid[])
		  AND (
			(
				status = 'pending'
				AND created_at < now() - ($1 * interval '1 second')
			)
			OR (
				status = 'running'
				AND run_started_at IS NOT NULL
				AND run_started_at < now() - ($2 * interval '1 second')
			)
			OR (
				status = 'ready_to_apply'
				AND updated_at < now() - ($3 * interval '1 second')
			)
		  )
	`, updatePendingTimeout.Seconds(), updateRunningTimeout.Seconds(), updateReadyTimeout.Seconds(), runtimeIDs); err != nil {
		return fmt.Errorf("expire stale daemon runtime updates: %w", err)
	}
	return nil
}

func (s *PostgresUpdateStore) HasPending(ctx context.Context, runtimeID string) (bool, error) {
	if err := s.requireDB(); err != nil {
		return false, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin pending daemon runtime update check: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.expireStale(ctx, tx, "runtime_id = $4", runtimeID); err != nil {
		return false, err
	}
	var pending bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM daemon_runtime_update
			WHERE runtime_id = $1
			  AND status = 'pending'
		)
	`, runtimeID).Scan(&pending); err != nil {
		return false, fmt.Errorf("check pending daemon runtime update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit pending daemon runtime update check: %w", err)
	}
	return pending, nil
}

// PopPending atomically claims the oldest pending request for one runtime.
// SKIP LOCKED keeps concurrent heartbeat handlers from serially returning the
// same update while still allowing unrelated runtimes to progress.
func (s *PostgresUpdateStore) PopPending(ctx context.Context, runtimeID string) (*UpdateRequest, error) {
	if err := s.requireDB(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim pending daemon runtime update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.expireStale(ctx, tx, "runtime_id = $4", runtimeID); err != nil {
		return nil, err
	}
	req, err := scanPostgresUpdate(tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM daemon_runtime_update
			WHERE runtime_id = $1
			  AND status = 'pending'
			ORDER BY created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE daemon_runtime_update update_request
		SET
			status = 'running',
			run_started_at = now(),
			updated_at = now()
		FROM candidate
		WHERE update_request.id = candidate.id
		RETURNING
			update_request.id,
			update_request.runtime_id::text,
			update_request.status,
			update_request.target_version,
			update_request.output,
			update_request.error,
			update_request.run_started_at,
			update_request.created_at,
			update_request.updated_at
	`, runtimeID))
	if err != nil {
		return nil, fmt.Errorf("claim pending daemon runtime update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim pending daemon runtime update: %w", err)
	}
	return req, nil
}

func (s *PostgresUpdateStore) Complete(ctx context.Context, id string, output string) error {
	return s.transition(ctx, id, UpdateCompleted, output, "", []UpdateStatus{UpdateRunning, UpdateReady})
}

func (s *PostgresUpdateStore) ReadyToApply(ctx context.Context, id string, output string) error {
	return s.transition(ctx, id, UpdateReady, output, "", []UpdateStatus{UpdateRunning})
}

func (s *PostgresUpdateStore) Fail(ctx context.Context, id string, errMsg string) error {
	// ready_to_apply → failed allowed for #815 path A (drain_timeout abandon).
	return s.transition(ctx, id, UpdateFailed, "", errMsg, []UpdateStatus{UpdateRunning, UpdateReady})
}

func (s *PostgresUpdateStore) transition(
	ctx context.Context,
	id string,
	status UpdateStatus,
	output string,
	errMsg string,
	allowedFrom []UpdateStatus,
) error {
	if err := s.requireDB(); err != nil {
		return err
	}
	if err := s.expireStale(ctx, s.db, "id = $4", id); err != nil {
		return err
	}
	allowed := make([]string, 0, len(allowedFrom))
	for _, from := range allowedFrom {
		allowed = append(allowed, string(from))
	}
	var currentStatus string
	var changed bool
	if err := s.db.QueryRow(ctx, `
		WITH current AS (
			SELECT status
			FROM daemon_runtime_update
			WHERE id = $1
			FOR UPDATE
		),
		transitioned AS (
			UPDATE daemon_runtime_update update_request
			SET
				status = $2,
				output = $3,
				error = $4,
				updated_at = now()
			FROM current
			WHERE update_request.id = $1
			  AND current.status = ANY($5::text[])
			RETURNING update_request.id
		)
		SELECT
			COALESCE((SELECT status FROM current), ''),
			EXISTS (SELECT 1 FROM transitioned)
	`, id, string(status), output, errMsg, allowed).Scan(&currentStatus, &changed); err != nil {
		return fmt.Errorf("transition daemon runtime update to %s: %w", status, err)
	}
	if changed || currentStatus == "" || currentStatus == string(status) {
		return nil
	}
	return invalidUpdateTransition(UpdateStatus(currentStatus), status)
}

func (s *PostgresUpdateStore) expireStale(
	ctx context.Context,
	exec dbExecutor,
	predicate string,
	value string,
) error {
	if _, err := exec.Exec(ctx, `
		UPDATE daemon_runtime_update
		SET
			status = 'timeout',
			error = CASE status
				WHEN 'pending' THEN 'daemon did not respond within 120 seconds'
				WHEN 'ready_to_apply' THEN 'activation did not complete within 20 minutes'
				ELSE 'update did not complete within 150 seconds'
			END,
			updated_at = now()
		WHERE `+predicate+`
		  AND (
			(
				status = 'pending'
				AND created_at < now() - ($1 * interval '1 second')
			)
			OR (
				status = 'running'
				AND run_started_at IS NOT NULL
				AND run_started_at < now() - ($2 * interval '1 second')
			)
			OR (
				status = 'ready_to_apply'
				AND updated_at < now() - ($3 * interval '1 second')
			)
		  )
	`, updatePendingTimeout.Seconds(), updateRunningTimeout.Seconds(), updateReadyTimeout.Seconds(), value); err != nil {
		return fmt.Errorf("expire stale daemon runtime update: %w", err)
	}
	return nil
}

func (s *PostgresUpdateStore) requireDB() error {
	if s == nil || s.db == nil {
		return errors.New("daemon runtime update PostgreSQL store is not configured")
	}
	return nil
}

const postgresUpdateSelect = `
	SELECT
		id,
		runtime_id::text,
		status,
		target_version,
		output,
		error,
		run_started_at,
		created_at,
		updated_at
	FROM daemon_runtime_update
`

type postgresUpdateScanner interface {
	Scan(dest ...any) error
}

func scanPostgresUpdate(row postgresUpdateScanner) (*UpdateRequest, error) {
	var req UpdateRequest
	var status string
	var runStartedAt pgtype.Timestamptz
	err := row.Scan(
		&req.ID,
		&req.RuntimeID,
		&status,
		&req.TargetVersion,
		&req.Output,
		&req.Error,
		&runStartedAt,
		&req.CreatedAt,
		&req.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	req.Status = UpdateStatus(status)
	if runStartedAt.Valid {
		startedAt := runStartedAt.Time
		req.RunStartedAt = &startedAt
	}
	return &req, nil
}

var _ UpdateStore = (*PostgresUpdateStore)(nil)
