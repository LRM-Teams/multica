package researchrun

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// freezeEvidenceRepresentationsTx replaces the legacy hash placeholder with
// the exact bounded wire representation supplied to an Agent. Only already
// authorized entries are read here.
func freezeEvidenceRepresentationsTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, entries []artifactVersionCandidate) error {
	authorized := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		authorized[entry.ArtifactID] = struct{}{}
	}
	for i := range entries {
		var value any
		switch entries[i].Kind {
		case ArtifactKindSourceSnapshot:
			var item SourceSnapshotView
			err := tx.QueryRow(ctx, `SELECT id::text, COALESCE(produced_by_task_id::text, ''), canonical_url, title, publisher, source_class, evidence_traits, independence_key, retrieved_at, content_hash, left(snapshot_text, $4), metadata, verification_status, created_at FROM research_source_snapshot WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID, sourceSnapshotExcerptChars).Scan(&item.ID, &item.ProducedByTaskID, &item.CanonicalURL, &item.Title, &item.Publisher, &item.SourceClass, &item.EvidenceTraits, &item.IndependenceKey, &item.RetrievedAt, &item.ContentHash, &item.SnapshotExcerpt, &item.Metadata, &item.VerificationStatus, &item.CreatedAt)
			if err != nil {
				return fmt.Errorf("freeze source representation: %w", err)
			}
			value = item
		case ArtifactKindObservation:
			var item Observation
			err := tx.QueryRow(ctx, `SELECT id::text, source_snapshot_id::text, COALESCE(produced_by_task_id::text, ''), quote, datum, locator, interpretation, verification_status, created_at FROM research_observation WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&item.ID, &item.SourceSnapshotID, &item.ProducedByTaskID, &item.Quote, &item.Datum, &item.Locator, &item.Interpretation, &item.VerificationStatus, &item.CreatedAt)
			if err != nil {
				return fmt.Errorf("freeze observation representation: %w", err)
			}
			value = item
		case ArtifactKindClaim:
			var item Claim
			err := tx.QueryRow(ctx, `SELECT id::text, COALESCE(produced_by_task_id::text, ''), client_key, evidence_standard_key, claim_text, significance, confidence, status, goal_version, plan_version, resolution, created_at, updated_at FROM research_claim WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&item.ID, &item.ProducedByTaskID, &item.ClientKey, &item.EvidenceStandardKey, &item.Text, &item.Significance, &item.Confidence, &item.Status, &item.GoalVersion, &item.PlanVersion, &item.Resolution, &item.CreatedAt, &item.UpdatedAt)
			if err != nil {
				return fmt.Errorf("freeze claim representation: %w", err)
			}
			item.Evidence = []ClaimEvidence{}
			rows, evidenceErr := tx.Query(ctx, `SELECT id::text, observation_id::text, relation, strength, directness, method_fit, verification_status, COALESCE(verified_by_task_id::text, ''), rationale FROM research_claim_evidence WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND claim_id=$3::uuid ORDER BY created_at, claim_id, observation_id, relation, id`, workspaceID, sessionID, item.ID)
			if evidenceErr != nil {
				return fmt.Errorf("freeze claim evidence: %w", evidenceErr)
			}
			for rows.Next() {
				var evidenceID string
				var evidence ClaimEvidence
				if evidenceErr = rows.Scan(&evidenceID, &evidence.ObservationID, &evidence.Relation, &evidence.Strength, &evidence.Directness, &evidence.MethodFit, &evidence.VerificationStatus, &evidence.VerifiedByTaskID, &evidence.Rationale); evidenceErr != nil {
					rows.Close()
					return fmt.Errorf("scan claim evidence: %w", evidenceErr)
				}
				if _, ok := authorized[evidenceID]; ok {
					item.Evidence = append(item.Evidence, evidence)
				}
			}
			evidenceErr = rows.Err()
			rows.Close()
			if evidenceErr != nil {
				return fmt.Errorf("read claim evidence: %w", evidenceErr)
			}
			value = item
		case ArtifactKindStageEvaluation:
			var item EvaluationPrivateContext
			err := tx.QueryRow(ctx, `SELECT id::text, stage, passed, score, findings, remediation, created_at FROM research_stage_eval WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, entries[i].ArtifactID).Scan(&item.ID, &item.Stage, &item.Passed, &item.Score, &item.Findings, &item.Remediation, &item.CreatedAt)
			if err != nil {
				return fmt.Errorf("freeze evaluation-private representation: %w", err)
			}
			value = item
		default:
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode %s representation: %w", entries[i].Kind, err)
		}
		entries[i].Representation = "full"
		entries[i].RepresentationBytes = encoded
		entries[i].RepresentationHash = contentHashFromPayload(encoded)
	}
	return nil
}

func loadFrozenEvaluationPrivatePool(ctx context.Context, pool *pgxpool.Pool, workspaceID, sessionID, attemptID string) ([]EvaluationPrivateContext, error) {
	rows, err := pool.Query(ctx, `
		SELECT e.representation, e.representation_bytes, e.representation_hash
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
		JOIN research_artifact_policy_grant g ON (g.workspace_id,g.session_id,g.id)=(m.workspace_id,m.session_id,m.evaluation_grant_id)
		JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
		JOIN research_artifact_passport p ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.attempt_id=$3::uuid
		  AND m.purpose='evaluation' AND g.status='active' AND g.revision=m.evaluation_grant_revision
		  AND g.evaluation_private=true AND p.entity_kind='stage_evaluation'
		ORDER BY e.ordinal
	`, workspaceID, sessionID, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvaluationPrivateContext{}
	for rows.Next() {
		var representation string
		var encoded []byte
		var storedHash string
		if err = rows.Scan(&representation, &encoded, &storedHash); err != nil {
			return nil, err
		}
		var item EvaluationPrivateContext
		if err = decodeFrozenRepresentation(representation, encoded, storedHash, &item); err != nil {
			return nil, fmt.Errorf("decode evaluation-private representation: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadFrozenEvaluationPrivateTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, manifestID string) ([]EvaluationPrivateContext, error) {
	rows, err := tx.Query(ctx, `
		SELECT e.representation, e.representation_bytes, e.representation_hash
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
		JOIN research_artifact_policy_grant g ON (g.workspace_id,g.session_id,g.id)=(m.workspace_id,m.session_id,m.evaluation_grant_id)
		JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
		JOIN research_artifact_passport p ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.id=$3::uuid
		  AND m.purpose='evaluation' AND g.status='active' AND g.revision=m.evaluation_grant_revision
		  AND g.evaluation_private=true AND p.entity_kind='stage_evaluation'
		ORDER BY e.ordinal
	`, workspaceID, sessionID, manifestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvaluationPrivateContext{}
	for rows.Next() {
		var representation string
		var encoded []byte
		var storedHash string
		if err = rows.Scan(&representation, &encoded, &storedHash); err != nil {
			return nil, err
		}
		var item EvaluationPrivateContext
		if err = decodeFrozenRepresentation(representation, encoded, storedHash, &item); err != nil {
			return nil, fmt.Errorf("decode evaluation-private representation: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadFrozenEvidenceRepresentationsPool(ctx context.Context, pool *pgxpool.Pool, workspaceID, sessionID, attemptID string) ([]SourceSnapshotView, []Observation, []Claim, error) {
	rows, err := pool.Query(ctx, `SELECT p.entity_kind, e.representation, e.representation_bytes, e.representation_hash FROM research_artifact_context_entry e JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id) JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id) JOIN research_artifact_passport p ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id) WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.attempt_id=$3::uuid AND p.entity_kind IN ('source_snapshot','observation','claim') ORDER BY e.ordinal`, workspaceID, sessionID, attemptID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	sources := []SourceSnapshotView{}
	observations := []Observation{}
	claims := []Claim{}
	for rows.Next() {
		var kind string
		var representation string
		var encoded []byte
		var storedHash string
		if err = rows.Scan(&kind, &representation, &encoded, &storedHash); err != nil {
			return nil, nil, nil, err
		}
		switch ArtifactEntityKind(kind) {
		case ArtifactKindSourceSnapshot:
			var item SourceSnapshotView
			if err = decodeFrozenRepresentation(representation, encoded, storedHash, &item); err != nil {
				return nil, nil, nil, fmt.Errorf("decode frozen source: %w", err)
			}
			sources = append(sources, item)
		case ArtifactKindObservation:
			var item Observation
			if err = decodeFrozenRepresentation(representation, encoded, storedHash, &item); err != nil {
				return nil, nil, nil, fmt.Errorf("decode frozen observation: %w", err)
			}
			observations = append(observations, item)
		case ArtifactKindClaim:
			var item Claim
			if err = decodeFrozenRepresentation(representation, encoded, storedHash, &item); err != nil {
				return nil, nil, nil, fmt.Errorf("decode frozen claim: %w", err)
			}
			claims = append(claims, item)
		}
	}
	return sources, observations, claims, rows.Err()
}

func decodeFrozenRepresentation(representation string, encoded []byte, storedHash string, target any) error {
	if representation != "full" || len(encoded) == 0 || contentHashFromPayload(encoded) != storedHash {
		return fmt.Errorf("%w: frozen artifact representation missing or hash mismatch", ErrInvalidTransition)
	}
	return json.Unmarshal(encoded, target)
}
