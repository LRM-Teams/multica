package researchrun

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// V6ActivationEvidenceRecord is the durable audit row for one activation
// requirement. Evidence is intentionally append-only at the database layer;
// a new revision supersedes an old one without rewriting history.
type V6ActivationEvidenceRecord struct {
	Requirement string
	EvidenceID  string
	Revision    string
	Passed      bool
	RecordedBy  string
}

// AssessPersistedV6Activation rebuilds the gate from durable audit rows.
// Report-origin policy remains runtime configuration and is supplied by the
// caller rather than duplicated in the evidence ledger.
func (s *PostgresStore) AssessPersistedV6Activation(ctx context.Context, origin V6ReportOriginEvidence, rollbackVersion string) (V6ActivationDecision, error) {
	rows, err := s.ListV6ActivationEvidence(ctx)
	if err != nil {
		return V6ActivationDecision{}, err
	}
	byRequirement := make(map[V6ActivationRequirement]V6GateEvidence, len(rows))
	for _, row := range rows {
		requirement := V6ActivationRequirement(row.Requirement)
		if _, known := activationRequirementSet[requirement]; !known {
			continue
		}
		if _, exists := byRequirement[requirement]; !exists {
			byRequirement[requirement] = V6GateEvidence{Passed: row.Passed, EvidenceID: row.EvidenceID, Revision: row.Revision}
		}
	}
	gate := func(requirement V6ActivationRequirement) V6GateEvidence { return byRequirement[requirement] }
	evidence := V6ActivationEvidence{
		Migrations: gate(V6RequirementMigrations), SchemaHash: gate(V6RequirementSchemaHash), LegacyGolden: gate(V6RequirementLegacyGolden), NineEnvelopes: gate(V6RequirementNineEnvelopes),
		RecoveryMatrix: gate(V6RequirementRecoveryMatrix), SingleSuccessorRace: gate(V6RequirementSingleSuccessorRace), DirectorContext: gate(V6RequirementDirectorContext), TeamLimit: gate(V6RequirementTeamLimit),
		KnowledgeGraph: gate(V6RequirementKnowledgeGraph), Discussion: gate(V6RequirementDiscussion), Steering: gate(V6RequirementSteering), ProjectionRebuild: gate(V6RequirementProjectionRebuild),
		ProjectionScale: gate(V6RequirementProjectionScale), GraphClients: gate(V6RequirementGraphClients), ReportSandboxWeb: gate(V6RequirementReportSandboxWeb), ReportSandboxDesktop: gate(V6RequirementReportSandboxDesktop),
		ReportOrigin: origin, BuiltinDocs: gate(V6RequirementBuiltinDocs), Rollback: V6RollbackEvidence{V6GateEvidence: gate(V6RequirementRollback), PreviousVersion: rollbackVersion},
	}
	return AssessV6Activation(evidence), nil
}

var activationRequirementSet = map[V6ActivationRequirement]struct{}{
	V6RequirementMigrations: {}, V6RequirementSchemaHash: {}, V6RequirementLegacyGolden: {}, V6RequirementNineEnvelopes: {}, V6RequirementRecoveryMatrix: {}, V6RequirementSingleSuccessorRace: {},
	V6RequirementDirectorContext: {}, V6RequirementTeamLimit: {}, V6RequirementKnowledgeGraph: {}, V6RequirementDiscussion: {}, V6RequirementSteering: {}, V6RequirementProjectionRebuild: {},
	V6RequirementProjectionScale: {}, V6RequirementGraphClients: {}, V6RequirementReportSandboxWeb: {}, V6RequirementReportSandboxDesktop: {}, V6RequirementReportOrigin: {}, V6RequirementBuiltinDocs: {}, V6RequirementRollback: {},
}

func validateV6ActivationEvidenceRecord(record V6ActivationEvidenceRecord) error {
	if strings.TrimSpace(record.Requirement) == "" || strings.TrimSpace(record.EvidenceID) == "" || strings.TrimSpace(record.Revision) == "" {
		return fmt.Errorf("%w: activation evidence requires requirement, evidence id, and revision", ErrInvalidContract)
	}
	if _, err := uuid.Parse(record.RecordedBy); err != nil {
		return fmt.Errorf("%w: activation evidence recorded_by must be a UUID", ErrInvalidContract)
	}
	return nil
}

// RecordV6ActivationEvidence persists reviewed evidence. It does not enable
// V6; release policy still consumes AssessV6Activation separately.
func (s *PostgresStore) RecordV6ActivationEvidence(ctx context.Context, record V6ActivationEvidenceRecord) error {
	if err := validateV6ActivationEvidenceRecord(record); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO research_v6_activation_evidence(requirement,evidence_id,revision,passed,recorded_by)
		VALUES($1,$2,$3,$4,$5::uuid)
		ON CONFLICT (requirement,evidence_id,revision) DO NOTHING`,
		record.Requirement, record.EvidenceID, record.Revision, record.Passed, record.RecordedBy)
	return err
}

// ListV6ActivationEvidence returns the complete audit history, newest first.
func (s *PostgresStore) ListV6ActivationEvidence(ctx context.Context) ([]V6ActivationEvidenceRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT requirement,evidence_id,revision,passed,recorded_by::text FROM research_v6_activation_evidence ORDER BY recorded_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]V6ActivationEvidenceRecord, 0)
	for rows.Next() {
		var record V6ActivationEvidenceRecord
		if err := rows.Scan(&record.Requirement, &record.EvidenceID, &record.Revision, &record.Passed, &record.RecordedBy); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
