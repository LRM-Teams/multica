// SPDX-License-Identifier: Apache-2.0

package service

// Manual Orchestrator service (spec §12.6, plan Slice 3.3): curator-only
// run creation over the ledger's single-active fence, attempt-fenced
// leases (migration 497), lease-guarded phase advancement whose side
// effects are recorded idempotently (response loss replays the recorded
// decision instead of re-executing), checkpoint recovery, and workspace
// reconciliation that only ever applies the safety terminals.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/skillevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SkillEvolutionOrchestratorService drives runs over the real ledger. It
// never materializes skills, never bypasses the ledger's CAS fences, and
// never auto-creates runs — the first milestone admits curator-created
// runs only (spec §12.6).
type SkillEvolutionOrchestratorService struct {
	pool        *pgxpool.Pool
	ledger      *PostgresSkillEvolutionLedger
	coordinator *skillevolution.RunCoordinator
	now         func() time.Time
}

func NewSkillEvolutionOrchestratorService(pool *pgxpool.Pool, ledger *PostgresSkillEvolutionLedger) *SkillEvolutionOrchestratorService {
	return &SkillEvolutionOrchestratorService{
		pool:        pool,
		ledger:      ledger,
		coordinator: skillevolution.NewRunCoordinator(ledger),
		now:         time.Now,
	}
}

// CreateManualRun admits one curator-created run with a complete pinned
// input set. Concurrent creation under the same evolution key surfaces
// ErrActiveRunExists — the ledger check and the migration-494 partial
// unique index are the fence, this method just routes through them.
func (s *SkillEvolutionOrchestratorService) CreateManualRun(
	ctx context.Context, creation skillevolution.ManualRunCreation,
) (skillevolution.EvolutionRunRecord, error) {
	record, err := skillevolution.CreateManualRun(creation)
	if err != nil {
		return skillevolution.EvolutionRunRecord{}, err
	}
	return s.coordinator.StartRun(ctx, record)
}

// AcquireLease takes the run's execution lease: attempt 1 on first
// acquisition, attempt+1 on re-acquisition once the previous lease
// expired. A live foreign lease is ErrLeaseHeld; terminal runs never
// lease.
func (s *SkillEvolutionOrchestratorService) AcquireLease(
	ctx context.Context, workspaceID, runID, ownerID string, ttl time.Duration,
) (skillevolution.RunLease, error) {
	if ttl <= 0 {
		return skillevolution.RunLease{}, fmt.Errorf("lease ttl must be positive")
	}
	workspaceUUID, runUUID, err := parseOrchestratorIDs(workspaceID, runID)
	if err != nil {
		return skillevolution.RunLease{}, err
	}
	run, err := s.ledger.GetRun(ctx, workspaceID, runID)
	if err != nil {
		return skillevolution.RunLease{}, err
	}
	if run.Status.Terminal() {
		return skillevolution.RunLease{}, fmt.Errorf(
			"%w: run %s is terminal (%s) and cannot be leased", skillevolution.ErrLedgerConflict, runID, run.Status)
	}
	row, err := db.New(s.pool).AcquireSkillEvolutionRunLease(ctx, db.AcquireSkillEvolutionRunLeaseParams{
		WorkspaceID: workspaceUUID, RunID: runUUID, OwnerID: ownerID, Column4: ttl.Seconds(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.RunLease{}, fmt.Errorf(
				"%w: run %s still holds a live lease", skillevolution.ErrLeaseHeld, runID)
		}
		return skillevolution.RunLease{}, fmt.Errorf("skill evolution orchestrator: acquire lease: %w", err)
	}
	return runLeaseFromRow(row), nil
}

// RenewLease extends the caller's own unexpired lease. A superseded or
// foreign lease fails closed; renewal of one's own EXPIRED lease is a
// conflict too — the worker re-acquires (new attempt) instead.
func (s *SkillEvolutionOrchestratorService) RenewLease(
	ctx context.Context, workspaceID, runID string, lease skillevolution.RunLease, ttl time.Duration,
) error {
	if ttl <= 0 {
		return fmt.Errorf("lease ttl must be positive")
	}
	workspaceUUID, runUUID, err := parseOrchestratorIDs(workspaceID, runID)
	if err != nil {
		return err
	}
	rows, err := db.New(s.pool).RenewSkillEvolutionRunLease(ctx, db.RenewSkillEvolutionRunLeaseParams{
		WorkspaceID: workspaceUUID, RunID: runUUID, OwnerID: lease.OwnerID, Attempt: lease.Attempt,
		Column5: ttl.Seconds(),
	})
	if err != nil {
		return fmt.Errorf("skill evolution orchestrator: renew lease: %w", err)
	}
	if rows == 1 {
		return nil
	}
	return s.classifyLeaseMiss(ctx, workspaceID, runID, lease, "renew")
}

// ReleaseLease expires the caller's own lease so the next acquisition can
// take over immediately. Releasing an already-expired own lease is an
// idempotent no-op; a superseded or foreign lease fails closed.
func (s *SkillEvolutionOrchestratorService) ReleaseLease(
	ctx context.Context, workspaceID, runID string, lease skillevolution.RunLease,
) error {
	workspaceUUID, runUUID, err := parseOrchestratorIDs(workspaceID, runID)
	if err != nil {
		return err
	}
	rows, err := db.New(s.pool).ReleaseSkillEvolutionRunLease(ctx, db.ReleaseSkillEvolutionRunLeaseParams{
		WorkspaceID: workspaceUUID, RunID: runUUID, OwnerID: lease.OwnerID, Attempt: lease.Attempt,
	})
	if err != nil {
		return fmt.Errorf("skill evolution orchestrator: release lease: %w", err)
	}
	if rows == 1 {
		return nil
	}
	return s.classifyLeaseMiss(ctx, workspaceID, runID, lease, "release")
}

func (s *SkillEvolutionOrchestratorService) classifyLeaseMiss(
	ctx context.Context, workspaceID, runID string, lease skillevolution.RunLease, verb string,
) error {
	durable, err := s.getLease(ctx, workspaceID, runID)
	if err != nil {
		if errors.Is(err, skillevolution.ErrLedgerNotFound) {
			return fmt.Errorf("%w: run %s has no lease to %s", skillevolution.ErrLeaseSuperseded, runID, verb)
		}
		return err
	}
	if durable.Attempt != lease.Attempt || durable.OwnerID != lease.OwnerID {
		return skillevolution.ClassifyLeaseFailure(*durable, lease)
	}
	// Same (owner, attempt): the lease simply expired under the caller.
	return fmt.Errorf("%w: lease on run %s expired at %s; re-acquire",
		skillevolution.ErrLedgerConflict, runID, durable.ExpiresAt.Format(time.RFC3339))
}

func (s *SkillEvolutionOrchestratorService) getLease(
	ctx context.Context, workspaceID, runID string,
) (*skillevolution.RunLease, error) {
	workspaceUUID, runUUID, err := parseOrchestratorIDs(workspaceID, runID)
	if err != nil {
		return nil, err
	}
	row, err := db.New(s.pool).GetSkillEvolutionRunLease(ctx, db.GetSkillEvolutionRunLeaseParams{
		WorkspaceID: workspaceUUID, RunID: runUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: run %s", skillevolution.ErrLedgerNotFound, runID)
		}
		return nil, fmt.Errorf("skill evolution orchestrator: get lease: %w", err)
	}
	lease := runLeaseFromRow(row)
	return &lease, nil
}

// PhaseStepOutcome is what one phase executor reports: the status the run
// moves to ("" = stay in the phase), an optional checkpoint for crash
// recovery, and an audit note. The concrete model-driven executors arrive
// with the proposer/consolidation adapters; the machinery below is what
// makes them safe.
type PhaseStepOutcome struct {
	NextStatus skillevolution.EvolutionRunStatus
	Checkpoint *skillevolution.RunCheckpoint
	Note       string
}

// orchestratorStepResponse is the idempotency-recorded decision of one
// leased step: after a response loss, the replay returns exactly this.
type orchestratorStepResponse struct {
	Status     string                        `json:"status"`
	Checkpoint *skillevolution.RunCheckpoint `json:"checkpoint,omitempty"`
}

// AdvanceRunPhase applies one leased step atomically: the lease is checked
// against the durable row, the status CAS and the idempotent decision
// record commit together, so a crash between them either replays the
// recorded decision or CAS-fails — never double-applies.
func (s *SkillEvolutionOrchestratorService) AdvanceRunPhase(
	ctx context.Context, workspaceID, runID string, lease skillevolution.RunLease, outcome PhaseStepOutcome,
) (skillevolution.EvolutionRunRecord, bool, error) {
	workspaceUUID, runUUID, err := parseOrchestratorIDs(workspaceID, runID)
	if err != nil {
		return skillevolution.EvolutionRunRecord{}, false, err
	}
	if err := lease.Validate(); err != nil {
		return skillevolution.EvolutionRunRecord{}, false, err
	}
	now := s.now()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return skillevolution.EvolutionRunRecord{}, false, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	run, err := q.GetSkillEvolutionRun(ctx, db.GetSkillEvolutionRunParams{WorkspaceID: workspaceUUID, ID: runUUID})
	if err != nil {
		return skillevolution.EvolutionRunRecord{}, false, mapLedgerNoRows(err, runID)
	}
	record := evolutionRunRecordFromRow(run)
	if record.Status.Terminal() {
		return skillevolution.EvolutionRunRecord{}, false, fmt.Errorf(
			"%w: run %s is terminal (%s)", skillevolution.ErrLedgerConflict, runID, record.Status)
	}

	// The durable lease decides; a superseded attempt never advances the
	// run, no matter what the writer believes.
	leaseRow, err := q.GetSkillEvolutionRunLease(ctx, db.GetSkillEvolutionRunLeaseParams{WorkspaceID: workspaceUUID, RunID: runUUID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.EvolutionRunRecord{}, false, fmt.Errorf(
				"%w: run %s has no durable lease", skillevolution.ErrLeaseHeld, runID)
		}
		return skillevolution.EvolutionRunRecord{}, false, fmt.Errorf("skill evolution orchestrator: step lease read: %w", err)
	}
	durable := runLeaseFromRow(leaseRow)
	if !skillevolution.LeaseMatches(durable, lease, now) {
		return skillevolution.EvolutionRunRecord{}, false, skillevolution.ClassifyLeaseFailure(durable, lease)
	}

	// A checkpoint records progress INSIDE the phase the run is in, under
	// the attempt that wrote it, derived from the run's own pin.
	if outcome.Checkpoint != nil {
		pinHash, err := skillevolution.RunPinnedInputsHash(record.PinnedInputs)
		if err != nil {
			return skillevolution.EvolutionRunRecord{}, false, err
		}
		checkpoint := *outcome.Checkpoint
		checkpoint.RunID = runID
		checkpoint.Phase = record.Status
		checkpoint.Attempt = lease.Attempt
		checkpoint.PinnedInputsHash = pinHash
		if checkpoint.RecordedAt.IsZero() {
			checkpoint.RecordedAt = now
		}
		if err := checkpoint.Validate(); err != nil {
			return skillevolution.EvolutionRunRecord{}, false, err
		}
		outcome.Checkpoint = &checkpoint
	}

	// The idempotency record is the response-loss guard: the key names
	// the step by its TARGET under the lease attempt (not by the run's
	// current status — after a response loss the run has already moved,
	// and the retry must land on the same row). Same key and payload
	// replays the recorded decision; same key and different payload is a
	// conflict, never an overwrite.
	stepKey := orchestratorStepKey(runID, stepTokenOf(outcome), lease.Attempt)
	response := orchestratorStepResponse{Status: string(record.Status)}
	if outcome.NextStatus != "" && outcome.NextStatus != record.Status {
		response.Status = string(outcome.NextStatus)
	}
	// The payload hash must be a function of the REQUEST's identity only:
	// target status, checkpoint summary, note. Everything the service
	// normalizes (checkpoint phase/attempt/pin) depends on the run's
	// mutable position, which differs between the first execution and a
	// response-loss retry — hashing those would break replay.
	checkpointSummary := ""
	if outcome.Checkpoint != nil {
		checkpointSummary = outcome.Checkpoint.Summary
		response.Checkpoint = outcome.Checkpoint
	}
	payloadHash := skillevolution.HashCanonicalPayload([]byte(fmt.Sprintf(
		"next=%q;checkpoint_summary=%q;note=%q", outcome.NextStatus, checkpointSummary, outcome.Note)))
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return skillevolution.EvolutionRunRecord{}, false, err
	}

	existing, err := q.GetSkillEvolutionIdempotency(ctx, db.GetSkillEvolutionIdempotencyParams{
		WorkspaceID: workspaceUUID, IdempotencyKey: stepKey,
	})
	switch {
	case err == nil:
		if existing.PayloadHash != payloadHash {
			return skillevolution.EvolutionRunRecord{}, false, fmt.Errorf(
				"%w: step %s already decided a different payload", skillevolution.ErrLedgerConflict, stepKey)
		}
		// Response loss: return the recorded decision without re-executing.
		if err := tx.Commit(ctx); err != nil {
			return skillevolution.EvolutionRunRecord{}, false, err
		}
		replayed, err := s.ledger.GetRun(ctx, workspaceID, runID)
		if err != nil {
			return skillevolution.EvolutionRunRecord{}, false, err
		}
		return replayed, true, nil
	case errors.Is(err, pgx.ErrNoRows):
		// First execution of this step.
	default:
		return skillevolution.EvolutionRunRecord{}, false, fmt.Errorf("skill evolution orchestrator: step lookup: %w", err)
	}

	if outcome.NextStatus != "" && outcome.NextStatus != record.Status {
		if !record.Status.CanTransition(outcome.NextStatus) {
			return skillevolution.EvolutionRunRecord{}, false, fmt.Errorf(
				"%w: run %s cannot move %s -> %s", skillevolution.ErrLedgerConflict, runID, record.Status, outcome.NextStatus)
		}
		rows, err := q.TransitionSkillEvolutionRunStatus(ctx, db.TransitionSkillEvolutionRunStatusParams{
			WorkspaceID: workspaceUUID, ID: runUUID,
			Status: string(outcome.NextStatus), Status_2: string(record.Status),
		})
		if err != nil {
			return skillevolution.EvolutionRunRecord{}, false, fmt.Errorf("skill evolution orchestrator: step transition: %w", err)
		}
		if rows != 1 {
			return skillevolution.EvolutionRunRecord{}, false, fmt.Errorf(
				"%w: run %s moved concurrently", skillevolution.ErrLedgerConflict, runID)
		}
	}

	if _, err := q.InsertSkillEvolutionIdempotency(ctx, db.InsertSkillEvolutionIdempotencyParams{
		WorkspaceID: workspaceUUID, IdempotencyKey: stepKey,
		RequestKind: "run_step", PayloadHash: payloadHash, Response: responseJSON,
	}); err != nil {
		return skillevolution.EvolutionRunRecord{}, false, fmt.Errorf("skill evolution orchestrator: step record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return skillevolution.EvolutionRunRecord{}, false, err
	}
	advanced, err := s.ledger.GetRun(ctx, workspaceID, runID)
	if err != nil {
		return skillevolution.EvolutionRunRecord{}, false, err
	}
	return advanced, false, nil
}

// RecoverRun reports what may safely happen to one run after a crash,
// response loss, or restart: the reconciler verdict plus, when a usable
// checkpoint exists, the resume point. It applies nothing — resumption is
// an explicit, lease-fenced operation.
func (s *SkillEvolutionOrchestratorService) RecoverRun(
	ctx context.Context, workspaceID, runID string,
) (skillevolution.ReconciliationDecision, error) {
	run, err := s.ledger.GetRun(ctx, workspaceID, runID)
	if err != nil {
		return skillevolution.ReconciliationDecision{}, err
	}
	checkpoints, err := s.runCheckpoints(ctx, workspaceID, runID)
	if err != nil {
		return skillevolution.ReconciliationDecision{}, err
	}
	lease, err := s.getLease(ctx, workspaceID, runID)
	if err != nil && !errors.Is(err, skillevolution.ErrLedgerNotFound) {
		return skillevolution.ReconciliationDecision{}, err
	}
	return skillevolution.ReconcileRun(skillevolution.ReconcilerInput{
		Now: s.now(), Run: run, Lease: lease, Checkpoints: checkpoints,
	})
}

// SkillEvolutionReconciliationSummary counts what one workspace sweep
// observed and did.
type SkillEvolutionReconciliationSummary struct {
	Examined int
	Awaited  int
	Resumed  int
	Requeued int
	Failed   int
	Stale    int
}

// ReconcileWorkspace sweeps the workspace's active runs and applies ONLY
// the safety terminals (failed/stale) that the reconciler mandates; resume
// and requeue stay decisions for the leased drivers. Every applied
// transition is a CAS, so concurrent sweeps cannot double-apply.
func (s *SkillEvolutionOrchestratorService) ReconcileWorkspace(
	ctx context.Context, workspaceID string, phaseDeadline time.Duration,
) (SkillEvolutionReconciliationSummary, error) {
	summary := SkillEvolutionReconciliationSummary{}
	workspaceUUID, err := parseLedgerUUID("workspace_id", workspaceID)
	if err != nil {
		return summary, err
	}
	runs, err := db.New(s.pool).ListActiveSkillEvolutionRuns(ctx, workspaceUUID)
	if err != nil {
		return summary, fmt.Errorf("skill evolution orchestrator: list active runs: %w", err)
	}
	q := db.New(s.pool)
	for _, runRow := range runs {
		record := evolutionRunRecordFromRow(runRow)
		summary.Examined++
		checkpoints, err := s.runCheckpoints(ctx, workspaceID, record.RunID)
		if err != nil {
			return summary, err
		}
		lease, err := s.getLease(ctx, workspaceID, record.RunID)
		if err != nil && !errors.Is(err, skillevolution.ErrLedgerNotFound) {
			return summary, err
		}
		decision, err := skillevolution.ReconcileRun(skillevolution.ReconcilerInput{
			Now: s.now(), Run: record, Lease: lease, Checkpoints: checkpoints, PhaseDeadline: phaseDeadline,
		})
		if err != nil {
			return summary, err
		}
		switch decision.Action {
		case skillevolution.ReconcileAwaitOwner:
			summary.Awaited++
		case skillevolution.ReconcileResumeCheckpoint:
			summary.Resumed++
		case skillevolution.ReconcileRequeuePhase:
			summary.Requeued++
		case skillevolution.ReconcileMarkFailed, skillevolution.ReconcileMarkStale:
			rows, err := q.TransitionSkillEvolutionRunStatus(ctx, db.TransitionSkillEvolutionRunStatusParams{
				WorkspaceID: workspaceUUID,
				ID:          runRow.ID,
				Status:      string(decision.NextStatus),
				Status_2:    string(record.Status),
			})
			if err != nil {
				return summary, fmt.Errorf("skill evolution orchestrator: reconcile transition: %w", err)
			}
			if rows == 1 {
				if decision.Action == skillevolution.ReconcileMarkFailed {
					summary.Failed++
				} else {
					summary.Stale++
				}
			}
		case skillevolution.ReconcileNone:
			// The run went terminal between the list and the decision; the
			// CAS would miss anyway. Nothing to count.
		}
	}
	if err := q.UpsertSkillEvolutionReconciliation(ctx, db.UpsertSkillEvolutionReconciliationParams{
		WorkspaceID: workspaceUUID, Lane: "orchestrator",
		LastReconciledAt: pgtype.Timestamptz{Time: s.now(), Valid: true},
		PendingCount:     int32(summary.Awaited + summary.Resumed + summary.Requeued),
	}); err != nil {
		return summary, fmt.Errorf("skill evolution orchestrator: reconciliation row: %w", err)
	}
	return summary, nil
}

// runCheckpoints decodes the checkpoints recorded under the run's
// idempotency step keys, newest first (the domain picks the newest).
func (s *SkillEvolutionOrchestratorService) runCheckpoints(
	ctx context.Context, workspaceID, runID string,
) ([]skillevolution.RunCheckpoint, error) {
	workspaceUUID, err := parseLedgerUUID("workspace_id", workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := db.New(s.pool).ListSkillEvolutionIdempotencyByKeyPrefix(ctx, db.ListSkillEvolutionIdempotencyByKeyPrefixParams{
		WorkspaceID: workspaceUUID,
		Column2:     pgtype.Text{String: orchestratorStepPrefix(runID), Valid: true},
		Limit:       32,
	})
	if err != nil {
		return nil, fmt.Errorf("skill evolution orchestrator: list checkpoints: %w", err)
	}
	checkpoints := make([]skillevolution.RunCheckpoint, 0, len(rows))
	for _, row := range rows {
		var response orchestratorStepResponse
		if err := json.Unmarshal(row.Response, &response); err != nil {
			return nil, fmt.Errorf("%w: run %s has an unreadable step record", skillevolution.ErrInvalidContract, runID)
		}
		if response.Checkpoint == nil {
			continue
		}
		checkpoints = append(checkpoints, *response.Checkpoint)
	}
	return checkpoints, nil
}

func orchestratorStepPrefix(runID string) string {
	return "run-step:" + runID + ":"
}

// stepTokenOf names one step within a lease attempt: where the step moves
// the run, or that it only recorded a checkpoint.
func stepTokenOf(outcome PhaseStepOutcome) string {
	if outcome.NextStatus == "" {
		return "checkpoint"
	}
	return string(outcome.NextStatus)
}

func orchestratorStepKey(runID, stepToken string, attempt int64) string {
	return orchestratorStepPrefix(runID) + stepToken + ":" + strconv.FormatInt(attempt, 10)
}

func runLeaseFromRow(row db.SkillEvolutionRunLease) skillevolution.RunLease {
	return skillevolution.RunLease{
		RunID:      row.RunID.String(),
		OwnerID:    row.OwnerID,
		Attempt:    row.Attempt,
		AcquiredAt: row.AcquiredAt.Time,
		ExpiresAt:  row.ExpiresAt.Time,
	}
}

func parseOrchestratorIDs(workspaceID, runID string) (pgtype.UUID, pgtype.UUID, error) {
	workspaceUUID, err := parseLedgerUUID("workspace_id", workspaceID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	runUUID, err := parseLedgerUUID("run_id", runID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	return workspaceUUID, runUUID, nil
}

func mapLedgerNoRows(err error, runID string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: run %s", skillevolution.ErrLedgerNotFound, runID)
	}
	return fmt.Errorf("skill evolution orchestrator: get run: %w", err)
}
