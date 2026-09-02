// SPDX-License-Identifier: Apache-2.0

package service

// PostgreSQL implementation of the skillevolution.EvaluationStore port
// (migration 493). Manifests replay idempotently by hash comparison,
// evaluation runs append with their per-assertion results in one
// transaction, and every read is workspace-scoped (ADR 0021 D7 package
// boundary).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/skillevolution"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

func jsonbOr(defaults []byte, value []byte) []byte {
	if len(value) == 0 {
		return defaults
	}
	return value
}

// InsertManifest persists one immutable manifest version. A zero-row
// conflict means the version exists: an identical hash replays as a no-op,
// anything else is a conflict, never an overwrite.
func (l *PostgresSkillEvolutionLedger) InsertManifest(ctx context.Context, manifest skillevolution.AssertionManifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	workspaceID, err := parseLedgerUUID("workspace_id", manifest.WorkspaceID)
	if err != nil {
		return err
	}
	contract, err := manifestContractJSON(manifest)
	if err != nil {
		return err
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	rows, err := q.InsertSkillAssertionManifest(ctx, db.InsertSkillAssertionManifestParams{
		WorkspaceID: workspaceID, ManifestID: manifest.ManifestID, Version: int32(manifest.Version),
		ManifestHash:    manifest.ManifestHash,
		DatasetIdentity: manifest.DatasetIdentity, DatasetVersion: manifest.DatasetVersion,
		LineageSplit: manifest.LineageSplit, DomainProfile: manifest.DomainProfile,
		TaskSlices:       jsonbOr([]byte(`[]`), manifest.TaskSlices),
		EvaluatorVersion: manifest.EvaluatorVersion, ScorerVersion: manifest.ScorerVersion,
		EnvironmentKey:       manifest.EnvironmentKey,
		RequiredCapabilities: jsonbOr([]byte(`[]`), manifest.RequiredCapabilities),
		DataResidency:        manifest.DataResidency,
		Contract:             contract,
		CreatedByActor:       manifest.CreatedByActor,
	})
	if err != nil {
		return fmt.Errorf("skill evaluation ledger: insert manifest: %w", err)
	}
	if rows == 0 {
		existing, err := q.GetSkillAssertionManifest(ctx, db.GetSkillAssertionManifestParams{
			WorkspaceID: workspaceID, ManifestID: manifest.ManifestID, Version: int32(manifest.Version),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: manifest %s version %d disappeared mid-insert",
					skillevolution.ErrLedgerConflict, manifest.ManifestID, manifest.Version)
			}
			return fmt.Errorf("skill evaluation ledger: reread manifest: %w", err)
		}
		if existing.ManifestHash != manifest.ManifestHash {
			return fmt.Errorf("%w: manifest %s version %d already exists with a different payload",
				skillevolution.ErrLedgerConflict, manifest.ManifestID, manifest.Version)
		}
		// Identical replay: the manifest and its assertions are already in
		// place, nothing to append.
		return tx.Commit(ctx)
	}
	for _, spec := range manifest.Assertions {
		if _, err := q.InsertSkillAssertion(ctx, db.InsertSkillAssertionParams{
			WorkspaceID: workspaceID, ManifestID: manifest.ManifestID,
			ManifestVersion: int32(manifest.Version), AssertionID: spec.AssertionID,
			Kind: spec.Kind, OracleRefHash: spec.OracleRefHash, Severity: spec.Severity,
			Hard: spec.Hard, Required: spec.Required, Tolerance: spec.Tolerance,
		}); err != nil {
			return fmt.Errorf("skill evaluation ledger: insert assertion %s: %w", spec.AssertionID, err)
		}
	}
	return tx.Commit(ctx)
}

func manifestContractJSON(manifest skillevolution.AssertionManifest) ([]byte, error) {
	// The contract column pins the manifest body for audit; the record's
	// own JSON encoding is the canonical serialization.
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("skill evaluation ledger: encode manifest contract: %w", err)
	}
	return encoded, nil
}

// GetManifest rebuilds the manifest record with its declared assertions.
func (l *PostgresSkillEvolutionLedger) GetManifest(ctx context.Context, workspaceIDStr, manifestID string, version int) (skillevolution.AssertionManifest, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return skillevolution.AssertionManifest{}, err
	}
	q := db.New(l.pool)
	row, err := q.GetSkillAssertionManifest(ctx, db.GetSkillAssertionManifestParams{
		WorkspaceID: workspaceID, ManifestID: manifestID, Version: int32(version),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.AssertionManifest{}, fmt.Errorf("%w: manifest %s version %d", skillevolution.ErrLedgerNotFound, manifestID, version)
		}
		return skillevolution.AssertionManifest{}, fmt.Errorf("skill evaluation ledger: get manifest: %w", err)
	}
	assertions, err := q.ListSkillAssertions(ctx, db.ListSkillAssertionsParams{
		WorkspaceID: workspaceID, ManifestID: manifestID, ManifestVersion: int32(version),
	})
	if err != nil {
		return skillevolution.AssertionManifest{}, fmt.Errorf("skill evaluation ledger: list assertions: %w", err)
	}
	record := skillevolution.AssertionManifest{
		ContractKind: "assertion_manifest", SchemaVersion: 1,
		ManifestID: row.ManifestID, Version: int(row.Version),
		WorkspaceID: row.WorkspaceID.String(), ManifestHash: row.ManifestHash,
		DatasetIdentity: row.DatasetIdentity, DatasetVersion: row.DatasetVersion,
		LineageSplit: row.LineageSplit, DomainProfile: row.DomainProfile,
		TaskSlices: row.TaskSlices, EvaluatorVersion: row.EvaluatorVersion,
		ScorerVersion: row.ScorerVersion, EnvironmentKey: row.EnvironmentKey,
		RequiredCapabilities: row.RequiredCapabilities, DataResidency: row.DataResidency,
		CreatedByActor: row.CreatedByActor, CreatedAt: row.CreatedAt.Time,
	}
	record.Assertions = make([]skillevolution.AssertionSpec, 0, len(assertions))
	for _, assertion := range assertions {
		record.Assertions = append(record.Assertions, skillevolution.AssertionSpec{
			AssertionID: assertion.AssertionID, Kind: assertion.Kind,
			OracleRefHash: assertion.OracleRefHash, Severity: assertion.Severity,
			Hard: assertion.Hard, Required: assertion.Required, Tolerance: assertion.Tolerance,
		})
	}
	return record, nil
}

// InsertEvaluationRun appends the run and its per-assertion results in one
// transaction. The DB-level scoped FKs reject cross-tenant candidates and
// results against assertions the pinned manifest version never declared.
func (l *PostgresSkillEvolutionLedger) InsertEvaluationRun(ctx context.Context, record skillevolution.EvaluationRunRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	workspaceID, err := parseLedgerUUID("workspace_id", record.WorkspaceID)
	if err != nil {
		return err
	}
	agentID, err := parseLedgerUUID("target_agent_id", record.TargetAgentID)
	if err != nil {
		return err
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if _, err := q.InsertSkillEvaluationRun(ctx, db.InsertSkillEvaluationRunParams{
		WorkspaceID: workspaceID, EvaluationID: record.EvaluationID,
		CandidateID: record.CandidateID, ManifestID: record.ManifestID,
		ManifestVersion:  int32(record.ManifestVersion),
		BaseArtifactHash: record.BaseArtifactHash, CandidateArtifactHash: record.CandidateArtifactHash,
		ManifestHash: record.ManifestHash, TargetAgentID: agentID,
		TargetModelID: record.TargetModelID, ProviderID: record.ProviderID,
		ToolCapabilityID: record.ToolCapabilityID, RuntimeID: record.RuntimeID,
		EnvironmentKey:        record.EnvironmentKey,
		Metrics:               jsonbOr([]byte(`{}`), record.Metrics),
		ContaminationStatus:   string(record.Contamination),
		DecisionPolicyVersion: record.DecisionPolicyVersion,
		TerminalResult:        string(record.TerminalResult), TerminalReason: record.TerminalReason,
		CreatedByActor: record.CreatedByActor,
	}); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: evaluation %s already exists", skillevolution.ErrLedgerConflict, record.EvaluationID)
		}
		return fmt.Errorf("skill evaluation ledger: insert evaluation run: %w", err)
	}
	for _, result := range record.AssertionResults {
		if _, err := q.InsertSkillEvaluationAssertionResult(ctx, db.InsertSkillEvaluationAssertionResultParams{
			WorkspaceID: workspaceID, EvaluationID: record.EvaluationID,
			ManifestID: record.ManifestID, ManifestVersion: int32(record.ManifestVersion),
			AssertionID: result.AssertionID, Result: string(result.Result),
			EvidenceHash: result.EvidenceHash,
		}); err != nil {
			return fmt.Errorf("skill evaluation ledger: insert result %s: %w", result.AssertionID, err)
		}
	}
	return tx.Commit(ctx)
}

func evaluationRunRecordFromRow(row db.SkillEvaluationRun, results []db.SkillEvaluationAssertionResult) skillevolution.EvaluationRunRecord {
	record := skillevolution.EvaluationRunRecord{
		ContractKind: "evaluation_run", SchemaVersion: 1,
		EvaluationID: row.EvaluationID, WorkspaceID: row.WorkspaceID.String(),
		CandidateID: row.CandidateID, ManifestID: row.ManifestID,
		ManifestVersion:  int(row.ManifestVersion),
		BaseArtifactHash: row.BaseArtifactHash, CandidateArtifactHash: row.CandidateArtifactHash,
		ManifestHash: row.ManifestHash, TargetAgentID: row.TargetAgentID.String(),
		TargetModelID: row.TargetModelID, ProviderID: row.ProviderID,
		ToolCapabilityID: row.ToolCapabilityID, RuntimeID: row.RuntimeID,
		EnvironmentKey: row.EnvironmentKey, Metrics: row.Metrics,
		Contamination:         skillevolution.ContaminationStatus(row.ContaminationStatus),
		DecisionPolicyVersion: row.DecisionPolicyVersion,
		TerminalResult:        skillevolution.EvaluationTerminalResult(row.TerminalResult),
		TerminalReason:        row.TerminalReason,
		CreatedByActor:        row.CreatedByActor, CreatedAt: row.CreatedAt.Time,
	}
	record.AssertionResults = make([]skillevolution.AssertionResult, 0, len(results))
	for _, result := range results {
		record.AssertionResults = append(record.AssertionResults, skillevolution.AssertionResult{
			AssertionID:  result.AssertionID,
			Result:       skillevolution.EvaluationAssertionResult(result.Result),
			EvidenceHash: result.EvidenceHash,
		})
	}
	return record
}

func (l *PostgresSkillEvolutionLedger) evaluationRun(ctx context.Context, workspaceID pgtype.UUID, evaluationID string) (skillevolution.EvaluationRunRecord, error) {
	q := db.New(l.pool)
	row, err := q.GetSkillEvaluationRun(ctx, db.GetSkillEvaluationRunParams{
		WorkspaceID: workspaceID, EvaluationID: evaluationID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.EvaluationRunRecord{}, fmt.Errorf("%w: evaluation %s", skillevolution.ErrLedgerNotFound, evaluationID)
		}
		return skillevolution.EvaluationRunRecord{}, fmt.Errorf("skill evaluation ledger: get evaluation: %w", err)
	}
	results, err := q.ListSkillEvaluationAssertionResults(ctx, db.ListSkillEvaluationAssertionResultsParams{
		WorkspaceID: workspaceID, EvaluationID: evaluationID,
	})
	if err != nil {
		return skillevolution.EvaluationRunRecord{}, fmt.Errorf("skill evaluation ledger: list results: %w", err)
	}
	return evaluationRunRecordFromRow(row, results), nil
}

func (l *PostgresSkillEvolutionLedger) GetEvaluationRun(ctx context.Context, workspaceIDStr, evaluationID string) (skillevolution.EvaluationRunRecord, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return skillevolution.EvaluationRunRecord{}, err
	}
	return l.evaluationRun(ctx, workspaceID, evaluationID)
}

func (l *PostgresSkillEvolutionLedger) ListEvaluationRunsByCandidate(ctx context.Context, workspaceIDStr, candidateID string) ([]skillevolution.EvaluationRunRecord, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return nil, err
	}
	q := db.New(l.pool)
	rows, err := q.ListSkillEvaluationRunsByCandidate(ctx, db.ListSkillEvaluationRunsByCandidateParams{
		WorkspaceID: workspaceID, CandidateID: candidateID,
	})
	if err != nil {
		return nil, fmt.Errorf("skill evaluation ledger: list evaluations: %w", err)
	}
	records := make([]skillevolution.EvaluationRunRecord, 0, len(rows))
	for _, row := range rows {
		results, err := q.ListSkillEvaluationAssertionResults(ctx, db.ListSkillEvaluationAssertionResultsParams{
			WorkspaceID: workspaceID, EvaluationID: row.EvaluationID,
		})
		if err != nil {
			return nil, fmt.Errorf("skill evaluation ledger: list results for %s: %w", row.EvaluationID, err)
		}
		records = append(records, evaluationRunRecordFromRow(row, results))
	}
	return records, nil
}
