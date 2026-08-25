// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
)

// GraphMemoryDiveJob is one leased Dive job (spec §5/§6): it mirrors the
// recall's identity and pinned graph version (storage-level trigger enforced)
// and carries the attempt count for bounded retries.
type GraphMemoryDiveJob struct {
	ID           string
	RecallID     string
	WorkspaceID  string
	TraceID      string
	GraphKind    string
	GraphOwnerID string
	GraphVersion int
	Attempts     int
}

// GraphMemoryDiveService owns the durable Dive job queue: the K-terminal
// barrier enqueue, worker leasing with fencing, bounded retries, and the
// terminal judge_failed path (A8/A25).
type GraphMemoryDiveService struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryDiveService(pool *pgxpool.Pool) *GraphMemoryDiveService {
	return &GraphMemoryDiveService{pool: pool}
}

// EnqueueIfBarrierMet inserts the recall's dive job once every explore
// trajectory is terminal. It is idempotent by recall (one job ever) and a
// no-op for terminal recalls or recalls whose job already exists.
func (s *GraphMemoryDiveService) EnqueueIfBarrierMet(ctx context.Context, recallID string) (bool, error) {
	rUUID, err := util.ParseUUID(recallID)
	if err != nil {
		return false, fmt.Errorf("graph memory dive: recall id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status, traceID, kind string
		version               int
		ws, owner             pgtype.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT status, trace_id, graph_kind, graph_owner_id, graph_version, workspace_id
		FROM graph_memory_recall WHERE id = $1 FOR UPDATE
	`, rUUID).Scan(&status, &traceID, &kind, &owner, &version, &ws)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("graph memory dive: unknown recall %s", recallID)
	case err != nil:
		return false, fmt.Errorf("graph memory dive: load recall: %w", err)
	}
	switch status {
	case "accepted", "exploring", "explore_terminal":
		// Barrier may be met below.
	default:
		// dive_queued/diving: already enqueued; terminal: nothing to judge.
		return false, nil
	}

	var running int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM graph_memory_trajectory WHERE recall_id = $1 AND status = 'running'
	`, rUUID).Scan(&running); err != nil {
		return false, fmt.Errorf("graph memory dive: count running trajectories: %w", err)
	}
	if running > 0 {
		return false, nil
	}

	var jobID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO graph_memory_dive_job (recall_id, workspace_id, trace_id, graph_kind, graph_owner_id, graph_version)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (recall_id) DO NOTHING
		RETURNING id
	`, rUUID, ws, traceID, kind, owner, version).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("graph memory dive: insert job: %w", err)
	}
	if _, err := acquireVersionLease(ctx, tx, kind, util.UUIDToString(owner), version, "dive", util.UUIDToString(jobID)); err != nil {
		return false, fmt.Errorf("graph memory dive: acquire version lease: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_recall SET status = 'dive_queued', updated_at = now()
		WHERE id = $1 AND status IN ('accepted', 'exploring', 'explore_terminal')
	`, rUUID); err != nil {
		return false, fmt.Errorf("graph memory dive: mark recall dive_queued: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// Lease atomically claims one leasable job for workerID: queued jobs first,
// then expired leases (crash recovery), oldest first. Jobs whose lease
// expired with attempts exhausted are terminalized as judge_failed instead.
// The recall's transition to diving rides the same transaction.
func (s *GraphMemoryDiveService) Lease(ctx context.Context, workerID string, ttl time.Duration) (*GraphMemoryDiveJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Crash on the final attempt: the expired lease can never be re-acquired,
	// so the job terminalizes here (bounded retries, A8).
	reaped, err := graphMemoryDiveReapExhausted(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, recallID := range reaped {
		if err := graphMemoryDiveTerminalFailRecall(ctx, tx, recallID); err != nil {
			return nil, err
		}
	}

	var (
		job                            GraphMemoryDiveJob
		jobID, recallID, wsID, ownerID pgtype.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT id, recall_id, workspace_id, trace_id, graph_kind, graph_owner_id, graph_version, attempts
		FROM graph_memory_dive_job
		WHERE (status = 'queued' OR (status = 'running' AND lease_expires_at < now()))
		  AND attempts < max_attempts
		ORDER BY created_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`).Scan(&jobID, &recallID, &wsID, &job.TraceID, &job.GraphKind, &ownerID, &job.GraphVersion, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("graph memory dive: lease candidate: %w", err)
	}

	expiry := time.Now().Add(ttl)
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_dive_job
		SET status = 'running', leased_by = $2, lease_expires_at = $3,
		    attempts = attempts + 1, updated_at = now()
		WHERE id = $1
	`, jobID, workerID, expiry); err != nil {
		return nil, fmt.Errorf("graph memory dive: lease: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_recall SET status = 'diving', updated_at = now()
		WHERE id = $1 AND status IN ('dive_queued', 'diving')
	`, recallID); err != nil {
		return nil, fmt.Errorf("graph memory dive: mark recall diving: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	job.ID = util.UUIDToString(jobID)
	job.RecallID = util.UUIDToString(recallID)
	job.WorkspaceID = util.UUIDToString(wsID)
	job.GraphOwnerID = util.UUIDToString(ownerID)
	job.Attempts++
	return &job, nil
}

// Complete terminalizes the job and its recall, fenced on the worker's live
// lease: a stale or expired worker observes ok=false and no state changes.
func (s *GraphMemoryDiveService) Complete(ctx context.Context, jobID, workerID string, incomplete bool, result []byte) (bool, error) {
	jUUID, err := util.ParseUUID(jobID)
	if err != nil {
		return false, fmt.Errorf("graph memory dive: job id: %w", err)
	}
	if len(result) == 0 {
		result = []byte("{}")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var recallID pgtype.UUID
	err = tx.QueryRow(ctx, `
		UPDATE graph_memory_dive_job
		SET status = 'completed', incomplete = $3, result = $4,
		    leased_by = '', lease_expires_at = NULL, terminal_at = now(), updated_at = now()
		WHERE id = $1 AND leased_by = $2 AND status = 'running' AND lease_expires_at > now()
		RETURNING recall_id
	`, jUUID, workerID, incomplete, string(result)).Scan(&recallID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("graph memory dive: complete: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_recall SET status = 'completed', terminal_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('dive_queued', 'diving')
	`, recallID); err != nil {
		return false, fmt.Errorf("graph memory dive: mark recall completed: %w", err)
	}
	if err := releaseDiveVersionLease(ctx, tx, jUUID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// Fail records a worker failure, fenced on the worker's live lease. A
// retryable failure re-queues the job while attempts remain; exhausting the
// bounded retries (or a non-retryable failure) terminalizes the job and
// moves the recall to judge_failed with reward 0 for normally completed
// runs and no ground truth (A8).
func (s *GraphMemoryDiveService) Fail(ctx context.Context, jobID, workerID, errorKind, lastError string, retryable bool) (bool, error) {
	jUUID, err := util.ParseUUID(jobID)
	if err != nil {
		return false, fmt.Errorf("graph memory dive: job id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		recallID             pgtype.UUID
		attempts, maxAttempt int
	)
	err = tx.QueryRow(ctx, `
		SELECT recall_id, attempts, max_attempts FROM graph_memory_dive_job
		WHERE id = $1 AND leased_by = $2 AND status = 'running'
		FOR UPDATE
	`, jUUID, workerID).Scan(&recallID, &attempts, &maxAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("graph memory dive: load leased job: %w", err)
	}

	if retryable && attempts < maxAttempt {
		if _, err := tx.Exec(ctx, `
			UPDATE graph_memory_dive_job
			SET status = 'queued', leased_by = '', lease_expires_at = NULL,
			    error_kind = $2, last_error = $3, updated_at = now()
			WHERE id = $1
		`, jUUID, errorKind, lastError); err != nil {
			return false, fmt.Errorf("graph memory dive: requeue: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE graph_memory_recall SET status = 'dive_queued', updated_at = now()
			WHERE id = $1 AND status = 'diving'
		`, recallID); err != nil {
			return false, fmt.Errorf("graph memory dive: recall requeue: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_dive_job
		SET status = 'failed', error_kind = $2, last_error = $3,
		    leased_by = '', lease_expires_at = NULL, terminal_at = now(), updated_at = now()
		WHERE id = $1
	`, jUUID, errorKind, lastError); err != nil {
		return false, fmt.Errorf("graph memory dive: terminal fail: %w", err)
	}
	if err := graphMemoryDiveTerminalFailRecall(ctx, tx, recallID); err != nil {
		return false, err
	}
	if err := releaseDiveVersionLease(ctx, tx, jUUID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// graphMemoryDiveReapExhausted terminalizes running jobs whose lease expired
// with no attempts left and returns their recall ids.
func graphMemoryDiveReapExhausted(ctx context.Context, tx pgx.Tx) ([]pgtype.UUID, error) {
	rows, err := tx.Query(ctx, `
		UPDATE graph_memory_dive_job
		SET status = 'failed', error_kind = 'infra',
		    last_error = 'lease expired with attempts exhausted',
		    leased_by = '', lease_expires_at = NULL, terminal_at = now(), updated_at = now()
		WHERE status = 'running' AND lease_expires_at < now() AND attempts >= max_attempts
		RETURNING id, recall_id
	`)
	if err != nil {
		return nil, fmt.Errorf("graph memory dive: reap exhausted leases: %w", err)
	}
	defer rows.Close()
	var (
		out    []pgtype.UUID
		jobIDs []pgtype.UUID
	)
	for rows.Next() {
		var jobID, recallID pgtype.UUID
		if err := rows.Scan(&jobID, &recallID); err != nil {
			return nil, err
		}
		jobIDs = append(jobIDs, jobID)
		out = append(out, recallID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, jobID := range jobIDs {
		if err := releaseDiveVersionLease(ctx, tx, jobID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func releaseDiveVersionLease(ctx context.Context, tx pgx.Tx, jobID pgtype.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_version_lease
		SET released_at = COALESCE(released_at, now())
		WHERE consumer_kind = 'dive' AND consumer_id = $1
	`, jobID); err != nil {
		return fmt.Errorf("graph memory dive: release version lease: %w", err)
	}
	return nil
}

// graphMemoryDiveTerminalFailRecall moves the recall to judge_failed and
// zeroes the rewards of its normally completed runs (A8: reward 0, no
// authoritative ground truth).
func graphMemoryDiveTerminalFailRecall(ctx context.Context, tx pgx.Tx, recallID pgtype.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_recall SET status = 'judge_failed', terminal_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('dive_queued', 'diving')
	`, recallID); err != nil {
		return fmt.Errorf("graph memory dive: mark recall judge_failed: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_trajectory SET reward = 0, dive_status = 'judge_failed', updated_at = now()
		WHERE recall_id = $1 AND status IN ('found', 'miss')
	`, recallID); err != nil {
		return fmt.Errorf("graph memory dive: zero normal-run rewards: %w", err)
	}
	return nil
}
