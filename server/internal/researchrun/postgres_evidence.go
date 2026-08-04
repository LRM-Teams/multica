package researchrun

import (
	"context"
)

const sourceSnapshotExcerptChars = 4096

func (s *PostgresStore) ListSourceSnapshots(ctx context.Context, sessionID string) ([]SourceSnapshotView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(produced_by_task_id::text, ''), canonical_url,
		       title, publisher, source_class, evidence_traits, independence_key, retrieved_at,
		       content_hash, left(snapshot_text, $2), metadata,
		       verification_status, created_at
		FROM research_source_snapshot
		WHERE session_id = $1::uuid
		ORDER BY created_at, id
	`, sessionID, sourceSnapshotExcerptChars)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceSnapshotView{}
	for rows.Next() {
		var item SourceSnapshotView
		if err = rows.Scan(
			&item.ID, &item.ProducedByTaskID, &item.CanonicalURL, &item.Title,
			&item.Publisher, &item.SourceClass, &item.EvidenceTraits, &item.IndependenceKey,
			&item.RetrievedAt, &item.ContentHash, &item.SnapshotExcerpt,
			&item.Metadata, &item.VerificationStatus, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListObservations(ctx context.Context, sessionID string) ([]Observation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, source_snapshot_id::text,
		       COALESCE(produced_by_task_id::text, ''), quote, datum, locator,
		       interpretation, verification_status, created_at
		FROM research_observation
		WHERE session_id = $1::uuid
		ORDER BY created_at, id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		var item Observation
		if err = rows.Scan(
			&item.ID, &item.SourceSnapshotID, &item.ProducedByTaskID,
			&item.Quote, &item.Datum, &item.Locator, &item.Interpretation,
			&item.VerificationStatus, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListClaims(ctx context.Context, sessionID string) ([]Claim, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(produced_by_task_id::text, ''), client_key, evidence_standard_key,
		       claim_text, significance, confidence, status, goal_version,
		       plan_version, resolution, created_at, updated_at
		FROM research_claim
		WHERE session_id = $1::uuid
		ORDER BY goal_version, plan_version, created_at, id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	claims := []Claim{}
	byID := map[string]int{}
	for rows.Next() {
		var item Claim
		if err = rows.Scan(
			&item.ID, &item.ProducedByTaskID, &item.ClientKey, &item.EvidenceStandardKey, &item.Text,
			&item.Significance, &item.Confidence, &item.Status, &item.GoalVersion,
			&item.PlanVersion, &item.Resolution, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		item.Evidence = []ClaimEvidence{}
		byID[item.ID] = len(claims)
		claims = append(claims, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	evidenceRows, err := s.pool.Query(ctx, `
		SELECT claim_id::text, observation_id::text, relation, strength, directness, method_fit,
		       verification_status, COALESCE(verified_by_task_id::text, ''), rationale
		FROM research_claim_evidence
		WHERE session_id = $1::uuid
		ORDER BY created_at, claim_id, observation_id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer evidenceRows.Close()
	for evidenceRows.Next() {
		var claimID string
		var item ClaimEvidence
		if err = evidenceRows.Scan(
			&claimID, &item.ObservationID, &item.Relation, &item.Strength, &item.Directness, &item.MethodFit,
			&item.VerificationStatus, &item.VerifiedByTaskID, &item.Rationale,
		); err != nil {
			return nil, err
		}
		if index, ok := byID[claimID]; ok {
			claims[index].Evidence = append(claims[index].Evidence, item)
		}
	}
	return claims, evidenceRows.Err()
}
