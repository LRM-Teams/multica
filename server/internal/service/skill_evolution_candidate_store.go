// SPDX-License-Identifier: Apache-2.0

package service

// Candidate-plane implementation of the skillevolution.CandidateStore
// port (migration 492, plan Phase 3 wrap-up): admission carries the
// immutable contract document (replay of the identical candidate is a
// no-op, the same id under a different contract is a conflict), reads
// rebuild the record from the contract plus the mutable status columns,
// and transitions are CAS'd with the DB terminal guard as the floor.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/skillevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// InsertCandidate persists one fresh needs_review candidate with its
// contract document and motivating-pattern links in a single transaction.
func (l *PostgresSkillEvolutionLedger) InsertCandidate(ctx context.Context, record skillevolution.SkillCandidateRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	workspaceID, err := parseLedgerUUID("workspace_id", record.WorkspaceID)
	if err != nil {
		return err
	}
	runID, err := parseLedgerUUID("run_id", record.RunID)
	if err != nil {
		return err
	}
	var targetSkillID pgtype.UUID
	if record.TargetSkillID != "" {
		targetSkillID, err = parseLedgerUUID("target_skill_id", record.TargetSkillID)
		if err != nil {
			return err
		}
	}
	// The contract document IS the record; its hash pins everything the
	// rejection memory and audit compare, including proposer identity.
	contract, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("skill evolution ledger: marshal candidate contract: %w", err)
	}
	contractHash := skillevolution.HashCanonicalPayload(contract)

	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	existing, err := q.GetSkillCandidate(ctx, db.GetSkillCandidateParams{
		WorkspaceID: workspaceID, CandidateID: record.CandidateID,
	})
	switch {
	case err == nil:
		if existing.ContractHash == contractHash {
			return nil // identical replay: a no-op, not a conflict
		}
		return fmt.Errorf("%w: candidate %s already admits a different contract",
			skillevolution.ErrLedgerConflict, record.CandidateID)
	case errors.Is(err, pgx.ErrNoRows):
		// First admission.
	default:
		return fmt.Errorf("skill evolution ledger: candidate lookup: %w", err)
	}

	rows, err := q.InsertSkillCandidate(ctx, db.InsertSkillCandidateParams{
		WorkspaceID: workspaceID, CandidateID: record.CandidateID, RunID: runID,
		TargetSkillID: targetSkillID, NewSkillName: record.NewSkillName,
		RequestedScope:   record.RequestedScope,
		BaseArtifactHash: record.BaseArtifactHash, CandidateArtifactHash: record.CandidateArtifactHash,
		ProposedDiffHash: record.ProposedDiffHash,
		ContractHash:     contractHash, Contract: contract,
		Status: string(record.Status), CurrentArtifactVersion: int32(record.CurrentArtifactVersion),
	})
	if err != nil {
		return fmt.Errorf("skill evolution ledger: insert candidate: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: insert candidate affected %d rows", skillevolution.ErrLedgerConflict, rows)
	}
	for _, patternID := range record.MotivatingPatterns {
		if _, err := q.InsertSkillCandidatePattern(ctx, db.InsertSkillCandidatePatternParams{
			WorkspaceID: workspaceID, CandidateID: record.CandidateID, PatternID: patternID,
		}); err != nil {
			return fmt.Errorf("skill evolution ledger: candidate pattern link: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// GetCandidate rebuilds the record: identity and status come from the
// columns (the CAS plane), proposer metadata from the immutable contract
// document.
func (l *PostgresSkillEvolutionLedger) GetCandidate(ctx context.Context, workspaceIDStr, candidateID string) (skillevolution.SkillCandidateRecord, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return skillevolution.SkillCandidateRecord{}, err
	}
	row, err := db.New(l.pool).GetSkillCandidate(ctx, db.GetSkillCandidateParams{
		WorkspaceID: workspaceID, CandidateID: candidateID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.SkillCandidateRecord{}, fmt.Errorf(
				"%w: candidate %s", skillevolution.ErrLedgerNotFound, candidateID)
		}
		return skillevolution.SkillCandidateRecord{}, fmt.Errorf("skill evolution ledger: get candidate: %w", err)
	}
	var record skillevolution.SkillCandidateRecord
	if err := json.Unmarshal(row.Contract, &record); err != nil {
		return skillevolution.SkillCandidateRecord{}, fmt.Errorf(
			"%w: candidate %s has an unreadable contract", skillevolution.ErrInvalidContract, candidateID)
	}
	// The columns are authoritative for everything mutable.
	record.WorkspaceID = row.WorkspaceID.String()
	record.CandidateID = row.CandidateID
	record.RunID = row.RunID.String()
	record.TargetSkillID = ""
	if row.TargetSkillID.Valid {
		record.TargetSkillID = row.TargetSkillID.String()
	}
	record.NewSkillName = row.NewSkillName
	record.RequestedScope = row.RequestedScope
	record.BaseArtifactHash = row.BaseArtifactHash
	record.CandidateArtifactHash = row.CandidateArtifactHash
	record.ProposedDiffHash = row.ProposedDiffHash
	record.Status = skillevolution.CandidateStatus(row.Status)
	record.CurrentArtifactVersion = int(row.CurrentArtifactVersion)
	return record, nil
}

// TransitionCandidateStatus CAS-updates the status; a miss is a conflict
// (the row moved or does not exist — the caller re-reads to tell them
// apart).
func (l *PostgresSkillEvolutionLedger) TransitionCandidateStatus(
	ctx context.Context, workspaceIDStr, candidateID string, from, to skillevolution.CandidateStatus,
) error {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return err
	}
	rows, err := db.New(l.pool).TransitionSkillCandidateStatus(ctx, db.TransitionSkillCandidateStatusParams{
		WorkspaceID: workspaceID, CandidateID: candidateID,
		Status: string(to), Status_2: string(from),
	})
	if err != nil {
		return fmt.Errorf("skill evolution ledger: transition candidate: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: candidate %s is not %s", skillevolution.ErrLedgerConflict, candidateID, from)
	}
	return nil
}
