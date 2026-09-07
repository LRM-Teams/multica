// SPDX-License-Identifier: Apache-2.0

package service

// PostgreSQL implementation of the skillevolution.TrajectoryEligibilityStore
// and BackfillCheckpointStore ports (migration 496). Pins append once and
// are frozen by the update guard trigger; revocation is the only later
// write and is a CAS on revoked_at IS NULL. Backfill checkpoints append
// immutable rows for both dry-run and executed passes (ADR 0021 D7
// package boundary).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/skillevolution"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

// PinEligibility persists the run-start snapshot. ON CONFLICT DO NOTHING
// makes re-pinning the same run idempotent; a different payload for the
// same run is a conflict, never an overwrite.
func (l *PostgresSkillEvolutionLedger) PinEligibility(ctx context.Context, eligibility skillevolution.TrajectoryEligibility) error {
	if err := eligibility.Validate(); err != nil {
		return err
	}
	if eligibility.Revoked() {
		return fmt.Errorf("%w: eligibility for run %s cannot be pinned already revoked",
			skillevolution.ErrInvalidContract, eligibility.RunID)
	}
	workspaceID, err := parseLedgerUUID("workspace_id", eligibility.WorkspaceID)
	if err != nil {
		return err
	}
	runID, err := parseLedgerUUID("run_id", eligibility.RunID)
	if err != nil {
		return err
	}
	purposes := make([]string, 0, len(eligibility.AllowedPurposes))
	for _, purpose := range eligibility.AllowedPurposes {
		purposes = append(purposes, string(purpose))
	}
	rows, err := db.New(l.pool).InsertSkillTrajectoryEligibility(ctx, db.InsertSkillTrajectoryEligibilityParams{
		WorkspaceID: workspaceID, RunID: runID, RunKind: eligibility.RunKind,
		EvolutionEligible: eligibility.EvolutionEligible, AllowedPurposes: purposes,
		TaskType: eligibility.TaskType, LineageID: eligibility.LineageID,
		FixedAt: pgTimestamptz(eligibility.FixedAt), FixedByActor: eligibility.FixedByActor,
	})
	if err != nil {
		return fmt.Errorf("skill trajectory eligibility: pin: %w", err)
	}
	if rows == 0 {
		existing, err := l.GetEligibility(ctx, eligibility.WorkspaceID, eligibility.RunID)
		if err != nil {
			return fmt.Errorf("%w: eligibility pin for run %s disappeared mid-insert",
				skillevolution.ErrLedgerConflict, eligibility.RunID)
		}
		if existing.RunKind != eligibility.RunKind ||
			existing.EvolutionEligible != eligibility.EvolutionEligible ||
			existing.TaskType != eligibility.TaskType ||
			existing.LineageID != eligibility.LineageID ||
			existing.FixedByActor != eligibility.FixedByActor ||
			!fixedAtEqualPostgres(existing.FixedAt, eligibility.FixedAt) ||
			existing.Revoked() {
			return fmt.Errorf("%w: eligibility for run %s already exists with a different pin",
				skillevolution.ErrLedgerConflict, eligibility.RunID)
		}
	}
	return nil
}

// GetEligibility resolves the pin for one run inside the workspace.
func (l *PostgresSkillEvolutionLedger) GetEligibility(ctx context.Context, workspaceID, runID string) (skillevolution.TrajectoryEligibility, error) {
	ws, err := parseLedgerUUID("workspace_id", workspaceID)
	if err != nil {
		return skillevolution.TrajectoryEligibility{}, err
	}
	rid, err := parseLedgerUUID("run_id", runID)
	if err != nil {
		return skillevolution.TrajectoryEligibility{}, err
	}
	row, err := db.New(l.pool).GetSkillTrajectoryEligibility(ctx, db.GetSkillTrajectoryEligibilityParams{
		WorkspaceID: ws, RunID: rid,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.TrajectoryEligibility{}, fmt.Errorf("%w: no eligibility pin for run %s",
				skillevolution.ErrLedgerNotFound, runID)
		}
		return skillevolution.TrajectoryEligibility{}, fmt.Errorf("skill trajectory eligibility: get: %w", err)
	}
	return eligibilityFromRow(row), nil
}

// fixedAtEqualPostgres compares pin times at PostgreSQL's microsecond
// resolution: a caller replaying the in-memory pin (nanoseconds) against
// the stored row (microseconds) is an identical replay, not a conflict.
func fixedAtEqualPostgres(a, b time.Time) bool {
	return a.Truncate(time.Microsecond).Equal(b.Truncate(time.Microsecond))
}

func eligibilityFromRow(row db.SkillTrajectoryEligibility) skillevolution.TrajectoryEligibility {
	purposes := make([]skillevolution.TrajectoryPurpose, 0, len(row.AllowedPurposes))
	for _, purpose := range row.AllowedPurposes {
		purposes = append(purposes, skillevolution.TrajectoryPurpose(purpose))
	}
	eligibility := skillevolution.TrajectoryEligibility{
		RunID:             row.RunID.String(),
		WorkspaceID:       row.WorkspaceID.String(),
		RunKind:           row.RunKind,
		EvolutionEligible: row.EvolutionEligible,
		AllowedPurposes:   purposes,
		TaskType:          row.TaskType,
		LineageID:         row.LineageID,
		FixedAt:           row.FixedAt.Time,
		FixedByActor:      row.FixedByActor,
		RevokedByActor:    row.RevokedByActor,
		RevokedReason:     row.RevokedReason,
	}
	if row.RevokedAt.Valid {
		eligibility.RevokedAt = row.RevokedAt.Time
	}
	return eligibility
}

// RevokeEligibility CAS-revokes a live pin. The statement matches only
// rows with revoked_at IS NULL and flips eligible=false in the same
// write; the update guard trigger enforces both invariants again.
func (l *PostgresSkillEvolutionLedger) RevokeEligibility(ctx context.Context, workspaceID, runID, actor, reason string, at time.Time) error {
	if actor == "" || reason == "" || at.IsZero() {
		return fmt.Errorf("%w: revocation needs an actor, a reason, and a time",
			skillevolution.ErrInvalidContract)
	}
	ws, err := parseLedgerUUID("workspace_id", workspaceID)
	if err != nil {
		return err
	}
	rid, err := parseLedgerUUID("run_id", runID)
	if err != nil {
		return err
	}
	rows, err := db.New(l.pool).RevokeSkillTrajectoryEligibility(ctx, db.RevokeSkillTrajectoryEligibilityParams{
		WorkspaceID: ws, RunID: rid, RevokedByActor: actor,
		RevokedAt: pgTimestamptz(at), RevokedReason: reason,
	})
	if err != nil {
		return fmt.Errorf("skill trajectory eligibility: revoke: %w", err)
	}
	if rows == 0 {
		if _, err := l.GetEligibility(ctx, workspaceID, runID); err != nil {
			return err
		}
		return fmt.Errorf("%w: eligibility for run %s is already revoked",
			skillevolution.ErrLedgerConflict, runID)
	}
	return nil
}

// RecordBackfillCheckpoint appends one job report. Replaying the same job
// id is a no-op; replaying it with different content is a conflict.
func (l *PostgresSkillEvolutionLedger) RecordBackfillCheckpoint(ctx context.Context, checkpoint skillevolution.BackfillCheckpoint) error {
	if err := checkpoint.Validate(); err != nil {
		return err
	}
	workspaceID, err := parseLedgerUUID("workspace_id", checkpoint.WorkspaceID)
	if err != nil {
		return err
	}
	rows, err := db.New(l.pool).InsertSkillBackfillCheckpoint(ctx, db.InsertSkillBackfillCheckpointParams{
		WorkspaceID: workspaceID, JobID: checkpoint.JobID,
		Kind: string(checkpoint.Kind), Mode: string(checkpoint.Mode), Actor: checkpoint.Actor,
		PolicyVersion: checkpoint.PolicyVersion, SourceWatermark: checkpoint.SourceWatermark,
		SelectedCount: int32(checkpoint.SelectedCount), RejectedCount: int32(checkpoint.RejectedCount),
		Reason: checkpoint.Reason,
	})
	if err != nil {
		return fmt.Errorf("skill backfill checkpoint: insert: %w", err)
	}
	if rows == 0 {
		existing, err := db.New(l.pool).GetSkillBackfillCheckpoint(ctx, db.GetSkillBackfillCheckpointParams{
			WorkspaceID: workspaceID, JobID: checkpoint.JobID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: backfill checkpoint %s disappeared mid-insert",
					skillevolution.ErrLedgerConflict, checkpoint.JobID)
			}
			return fmt.Errorf("skill backfill checkpoint: reread: %w", err)
		}
		if existing.Kind != string(checkpoint.Kind) || existing.Mode != string(checkpoint.Mode) ||
			existing.Actor != checkpoint.Actor ||
			existing.SelectedCount != int32(checkpoint.SelectedCount) ||
			existing.RejectedCount != int32(checkpoint.RejectedCount) {
			return fmt.Errorf("%w: backfill checkpoint %s already exists with different content",
				skillevolution.ErrLedgerConflict, checkpoint.JobID)
		}
	}
	return nil
}

// ListBackfillCheckpoints returns the newest job reports first.
func (l *PostgresSkillEvolutionLedger) ListBackfillCheckpoints(ctx context.Context, workspaceID string, limit int) ([]skillevolution.BackfillCheckpoint, error) {
	ws, err := parseLedgerUUID("workspace_id", workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.New(l.pool).ListSkillBackfillCheckpoints(ctx, db.ListSkillBackfillCheckpointsParams{
		WorkspaceID: ws, Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("skill backfill checkpoint: list: %w", err)
	}
	checkpoints := make([]skillevolution.BackfillCheckpoint, 0, len(rows))
	for _, row := range rows {
		checkpoints = append(checkpoints, skillevolution.BackfillCheckpoint{
			WorkspaceID:     row.WorkspaceID.String(),
			JobID:           row.JobID,
			Kind:            skillevolution.BackfillCheckpointKind(row.Kind),
			Mode:            skillevolution.BackfillMode(row.Mode),
			Actor:           row.Actor,
			PolicyVersion:   row.PolicyVersion,
			SourceWatermark: row.SourceWatermark,
			SelectedCount:   int(row.SelectedCount),
			RejectedCount:   int(row.RejectedCount),
			Reason:          row.Reason,
			CreatedAt:       row.CreatedAt.Time,
		})
	}
	return checkpoints, nil
}
