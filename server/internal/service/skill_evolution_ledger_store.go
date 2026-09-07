// SPDX-License-Identifier: Apache-2.0

package service

// PostgreSQL implementation of the skillevolution.LedgerStore port
// (migration 493). The domain package owns the contracts and the state
// machines; this file owns driver mapping, transactions, and CAS semantics
// (ADR 0021 D7 package boundary).

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/skillevolution"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

// PostgresSkillEvolutionLedger implements skillevolution.LedgerStore over
// the migration 493 tables.
type PostgresSkillEvolutionLedger struct {
	pool *pgxpool.Pool
}

func NewPostgresSkillEvolutionLedger(pool *pgxpool.Pool) *PostgresSkillEvolutionLedger {
	return &PostgresSkillEvolutionLedger{pool: pool}
}

func parseLedgerUUID(field, value string) (pgtype.UUID, error) {
	id, err := util.ParseUUID(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("skill evolution ledger: %s: %w", field, err)
	}
	return id, nil
}

// InsertRun persists one queued run. Single-active admission is
// check-then-insert here: the row-locked read serializes against other
// admissions through this store, and migration 495 adds the partial unique
// index that fences any writer the check cannot see.
func (l *PostgresSkillEvolutionLedger) InsertRun(ctx context.Context, run skillevolution.EvolutionRunRecord) error {
	workspaceID, err := parseLedgerUUID("workspace_id", run.WorkspaceID)
	if err != nil {
		return err
	}
	agentID, err := parseLedgerUUID("target_agent_id", run.TargetAgentID)
	if err != nil {
		return err
	}
	runID, err := parseLedgerUUID("run_id", run.RunID)
	if err != nil {
		return err
	}
	if len(run.PinnedInputs) == 0 {
		run.PinnedInputs = []byte(`{}`)
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	keyBody := skillevolution.EvolutionKey{
		TargetAgentID: run.TargetAgentID, TaskType: run.TaskType,
		EnvironmentMajorVersion: run.EnvironmentMajorVersion,
	}.Body()
	active, err := q.ListActiveSkillEvolutionRunsByKey(ctx, db.ListActiveSkillEvolutionRunsByKeyParams{
		WorkspaceID: workspaceID, EvolutionKey: pgtype.Text{String: keyBody, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("skill evolution ledger: active-run check: %w", err)
	}
	if len(active) > 0 {
		return fmt.Errorf("%w: run %s holds the key", skillevolution.ErrActiveRunExists, active[0].ID)
	}
	rows, err := q.InsertSkillEvolutionRun(ctx, db.InsertSkillEvolutionRunParams{
		ID: runID, WorkspaceID: workspaceID, TargetAgentID: agentID,
		TaskType: run.TaskType, EnvironmentMajorVersion: run.EnvironmentMajorVersion,
		Status:       string(skillevolution.EvolutionRunQueued),
		PinnedInputs: run.PinnedInputs, CreatedByActor: run.CreatedByActor,
	})
	if err != nil {
		return fmt.Errorf("skill evolution ledger: insert run: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: insert run affected %d rows", skillevolution.ErrLedgerConflict, rows)
	}
	return tx.Commit(ctx)
}

func (l *PostgresSkillEvolutionLedger) GetRun(ctx context.Context, workspaceIDStr, runIDStr string) (skillevolution.EvolutionRunRecord, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return skillevolution.EvolutionRunRecord{}, err
	}
	runID, err := parseLedgerUUID("run_id", runIDStr)
	if err != nil {
		return skillevolution.EvolutionRunRecord{}, err
	}
	row, err := db.New(l.pool).GetSkillEvolutionRun(ctx, db.GetSkillEvolutionRunParams{
		WorkspaceID: workspaceID, ID: runID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.EvolutionRunRecord{}, fmt.Errorf("%w: run %s", skillevolution.ErrLedgerNotFound, runIDStr)
		}
		return skillevolution.EvolutionRunRecord{}, fmt.Errorf("skill evolution ledger: get run: %w", err)
	}
	return evolutionRunRecordFromRow(row), nil
}

func evolutionRunRecordFromRow(row db.SkillEvolutionRun) skillevolution.EvolutionRunRecord {
	record := skillevolution.EvolutionRunRecord{
		RunID:                   row.ID.String(),
		WorkspaceID:             row.WorkspaceID.String(),
		TargetAgentID:           row.TargetAgentID.String(),
		TaskType:                row.TaskType,
		EnvironmentMajorVersion: row.EnvironmentMajorVersion,
		Status:                  skillevolution.EvolutionRunStatus(row.Status),
		PinnedInputs:            row.PinnedInputs,
		CreatedByActor:          row.CreatedByActor,
		CreatedAt:               row.CreatedAt.Time,
		UpdatedAt:               row.UpdatedAt.Time,
	}
	if row.TerminalAt.Valid {
		record.TerminalAt = &row.TerminalAt.Time
	}
	return record
}

// TransitionRun CAS-updates the status; a miss is a conflict (the row moved
// or does not exist — the caller re-reads to tell them apart).
func (l *PostgresSkillEvolutionLedger) TransitionRun(ctx context.Context, workspaceIDStr, runIDStr string, from, to skillevolution.EvolutionRunStatus) error {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return err
	}
	runID, err := parseLedgerUUID("run_id", runIDStr)
	if err != nil {
		return err
	}
	rows, err := db.New(l.pool).TransitionSkillEvolutionRunStatus(ctx, db.TransitionSkillEvolutionRunStatusParams{
		WorkspaceID: workspaceID, ID: runID, Status: string(to), Status_2: string(from),
	})
	if err != nil {
		// The terminal-guard trigger raises on revival attempts.
		return fmt.Errorf("%w: skill evolution ledger: transition run: %v", skillevolution.ErrLedgerConflict, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: CAS miss on run %s (%s)", skillevolution.ErrLedgerConflict, runIDStr, from)
	}
	return nil
}

// InsertPatternRevision validates the domain contract, then appends the
// immutable revision plus its evidence and advances the parent pointer in
// one transaction. Non-linear revisions are conflicts, never overwrites.
func (l *PostgresSkillEvolutionLedger) InsertPatternRevision(ctx context.Context, record skillevolution.PatternRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	workspaceID, err := parseLedgerUUID("workspace_id", record.WorkspaceID)
	if err != nil {
		return err
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if _, err := q.InsertSkillPatternIdentity(ctx, db.InsertSkillPatternIdentityParams{
		WorkspaceID: workspaceID, PatternID: record.PatternID,
		EvolutionKey: record.EvolutionKey, TaskType: record.TaskType,
		CurrentRevision: 0,
	}); err != nil {
		return fmt.Errorf("skill evolution ledger: pattern identity: %w", err)
	}
	parent, err := q.GetSkillPattern(ctx, db.GetSkillPatternParams{
		WorkspaceID: workspaceID, PatternID: record.PatternID,
	})
	if err != nil {
		return fmt.Errorf("skill evolution ledger: pattern parent: %w", err)
	}
	if parent.CurrentRevision+1 != record.Revision {
		return fmt.Errorf("%w: pattern %s revision %d is not linear after %d",
			skillevolution.ErrLedgerConflict, record.PatternID, record.Revision, parent.CurrentRevision)
	}
	if _, err := q.InsertSkillPatternRevision(ctx, db.InsertSkillPatternRevisionParams{
		WorkspaceID: workspaceID, PatternID: record.PatternID, Revision: record.Revision,
		PatternKind: string(record.PatternKind), Status: string(record.Status),
		Problem: record.Problem, Applicability: record.Applicability,
		RootCauseSummary: record.RootCauseSummary, RecommendedAction: record.RecommendedAction,
		TaskType: record.TaskType, SourceModelID: record.SourceModelID,
		TargetModelID: record.TargetModelID, ProviderID: record.ProviderID,
		ToolCapabilityID: record.ToolCapabilityID, RuntimeID: record.RuntimeID,
		EnvironmentKey: record.EnvironmentKey, GeneratorVersion: record.GeneratorVersion,
		PolicyVersion: record.PolicyVersion, ContentHash: record.ContentHash,
		CreatedByActor: record.CreatedByActor,
	}); err != nil {
		return fmt.Errorf("skill evolution ledger: pattern revision: %w", err)
	}
	for _, evidence := range append(append([]skillevolution.SkillEvolutionRef(nil), record.PositiveEvidence...), record.NegativeEvidence...) {
		polarity := "negative"
		if containsEvidenceRef(record.PositiveEvidence, evidence) {
			polarity = "positive"
		}
		if _, err := q.InsertSkillPatternEvidence(ctx, db.InsertSkillPatternEvidenceParams{
			WorkspaceID: workspaceID, PatternID: record.PatternID, Revision: record.Revision,
			Polarity: polarity, RefKind: string(evidence.Kind), RefID: evidence.ID,
			RefWorkspaceID: evidence.WorkspaceID,
		}); err != nil {
			return fmt.Errorf("skill evolution ledger: pattern evidence: %w", err)
		}
	}
	if _, err := q.AdvanceSkillPatternRevision(ctx, db.AdvanceSkillPatternRevisionParams{
		WorkspaceID: workspaceID, PatternID: record.PatternID,
		CurrentRevision: record.Revision, CurrentRevision_2: parent.CurrentRevision,
	}); err != nil {
		return fmt.Errorf("skill evolution ledger: advance pattern pointer: %w", err)
	}
	return tx.Commit(ctx)
}

func containsEvidenceRef(refs []skillevolution.SkillEvolutionRef, ref skillevolution.SkillEvolutionRef) bool {
	for _, candidate := range refs {
		if candidate == ref {
			return true
		}
	}
	return false
}

// LatestPatternRevision rebuilds the PatternRecord from the newest
// immutable revision and its evidence rows.
func (l *PostgresSkillEvolutionLedger) LatestPatternRevision(ctx context.Context, workspaceIDStr, patternID string) (skillevolution.PatternRecord, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return skillevolution.PatternRecord{}, err
	}
	q := db.New(l.pool)
	row, err := q.GetLatestSkillPatternRevision(ctx, db.GetLatestSkillPatternRevisionParams{
		WorkspaceID: workspaceID, PatternID: patternID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.PatternRecord{}, fmt.Errorf("%w: pattern %s", skillevolution.ErrLedgerNotFound, patternID)
		}
		return skillevolution.PatternRecord{}, fmt.Errorf("skill evolution ledger: latest pattern: %w", err)
	}
	parent, err := q.GetSkillPattern(ctx, db.GetSkillPatternParams{WorkspaceID: workspaceID, PatternID: patternID})
	if err != nil {
		return skillevolution.PatternRecord{}, fmt.Errorf("skill evolution ledger: pattern parent: %w", err)
	}
	evidence, err := q.ListSkillPatternEvidence(ctx, db.ListSkillPatternEvidenceParams{
		WorkspaceID: workspaceID, PatternID: patternID, Revision: row.Revision,
	})
	if err != nil {
		return skillevolution.PatternRecord{}, fmt.Errorf("skill evolution ledger: pattern evidence read: %w", err)
	}
	record := skillevolution.PatternRecord{
		ContractKind: "pattern", SchemaVersion: 1,
		PatternID: row.PatternID, Revision: row.Revision,
		WorkspaceID: row.WorkspaceID.String(), EvolutionKey: parent.EvolutionKey,
		PatternKind: skillevolution.PatternKind(row.PatternKind),
		Status:      skillevolution.PatternStatus(row.Status),
		Problem:     row.Problem, Applicability: row.Applicability,
		RootCauseSummary: row.RootCauseSummary, RecommendedAction: row.RecommendedAction,
		TaskType: row.TaskType, SourceModelID: row.SourceModelID,
		TargetModelID: row.TargetModelID, ProviderID: row.ProviderID,
		ToolCapabilityID: row.ToolCapabilityID, RuntimeID: row.RuntimeID,
		EnvironmentKey: row.EnvironmentKey, GeneratorVersion: row.GeneratorVersion,
		PolicyVersion: row.PolicyVersion, ContentHash: row.ContentHash,
		CreatedByActor: row.CreatedByActor, CreatedAt: row.CreatedAt.Time,
		UpdatedByActor: row.CreatedByActor, UpdatedAt: row.CreatedAt.Time,
	}
	for _, ref := range evidence {
		target := &record.NegativeEvidence
		if ref.Polarity == "positive" {
			target = &record.PositiveEvidence
		}
		*target = append(*target, skillevolution.SkillEvolutionRef{
			Kind: skillevolution.RefKind(ref.RefKind), ID: ref.RefID, WorkspaceID: ref.RefWorkspaceID,
		})
	}
	return record, nil
}

// ListPatternEvidence returns one revision's evidence refs (both
// polarities, kind-stable). The projection uses them as the retraction
// surface and the conflicts_with source; it never exposes them as
// aggregates.
func (l *PostgresSkillEvolutionLedger) ListPatternEvidence(ctx context.Context, workspaceIDStr, patternID string, revision int64) ([]skillevolution.SkillEvolutionRef, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return nil, err
	}
	rows, err := db.New(l.pool).ListSkillPatternEvidence(ctx, db.ListSkillPatternEvidenceParams{
		WorkspaceID: workspaceID, PatternID: patternID, Revision: revision,
	})
	if err != nil {
		return nil, fmt.Errorf("skill evolution ledger: list pattern evidence: %w", err)
	}
	refs := make([]skillevolution.SkillEvolutionRef, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		key := row.RefKind + "\x1f" + row.RefWorkspaceID + "\x1f" + row.RefID
		if seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, skillevolution.SkillEvolutionRef{
			Kind: skillevolution.RefKind(row.RefKind), ID: row.RefID, WorkspaceID: row.RefWorkspaceID,
		})
	}
	return refs, nil
}
