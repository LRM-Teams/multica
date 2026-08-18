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
