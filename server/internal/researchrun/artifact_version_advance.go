package researchrun

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type advanceArtifactVersionInput struct {
	WorkspaceID         string
	SessionID           string
	ArtifactID          string
	Kind                ArtifactEntityKind
	ContentHash         string
	AccessLevel         ArtifactAccessLevel
	GoalVersion         *int32
	PlanVersion         *int32
	ProducedByAttemptID string
}

// advanceArtifactVersionTx appends one immutable production version and moves
// the passport pointer with the exact reciprocal policy-ledger transition.
// The caller must persist the matching domain semantic change in the same
// Research transaction before commit.
func advanceArtifactVersionTx(ctx context.Context, tx pgx.Tx, in advanceArtifactVersionInput) (bool, error) {
	if in.ContentHash == "" {
		return false, fmt.Errorf("%w: artifact version advance requires a content hash", ErrInvalidContract)
	}
	if in.AccessLevel == "" {
		return false, fmt.Errorf("%w: artifact version advance requires an access level", ErrInvalidContract)
	}
	if err := ensureSessionPolicyStateTx(ctx, tx, in.WorkspaceID, in.SessionID); err != nil {
		return false, err
	}

	var entityKind, lifecycle, schemaName, schemaVersion, oldHash, oldAccess string
	var currentVersion int32
	var oldEligibility int64
	err := tx.QueryRow(ctx, `
		SELECT p.entity_kind, p.current_version, p.eligibility_revision, p.lifecycle_status,
		       v.schema_name, v.schema_version, v.content_hash, v.access_level
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON (v.workspace_id, v.session_id, v.artifact_id, v.version) =
		     (p.workspace_id, p.session_id, p.id, p.current_version)
		WHERE p.workspace_id = $1::uuid AND p.session_id = $2::uuid AND p.id = $3::uuid
		FOR UPDATE OF p
	`, in.WorkspaceID, in.SessionID, in.ArtifactID).Scan(
		&entityKind, &currentVersion, &oldEligibility, &lifecycle,
		&schemaName, &schemaVersion, &oldHash, &oldAccess,
	)
	if err != nil {
		return false, err
	}
	if entityKind != string(in.Kind) {
		return false, fmt.Errorf("%w: artifact kind is %q, want %q", ErrInvalidContract, entityKind, in.Kind)
	}
	if lifecycle != string(ArtifactLifecycleRegistered) && lifecycle != string(ArtifactLifecycleAccepted) {
		return false, fmt.Errorf("%w: cannot version artifact in lifecycle %q", ErrInvalidTransition, lifecycle)
	}
	if oldHash == in.ContentHash && oldAccess == string(in.AccessLevel) {
		return false, nil
	}
	if oldAccess != string(in.AccessLevel) {
		return false, fmt.Errorf("%w: semantic version advance cannot change artifact access from %q to %q", ErrInvalidTransition, oldAccess, in.AccessLevel)
	}

	newVersion := currentVersion + 1
	newEligibility := oldEligibility + 1
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_version (
			workspace_id, session_id, artifact_id, version, schema_name, schema_version,
			canonicalization_version, content_hash, access_level, goal_version, plan_version,
			hash_origin, produced_by_attempt_id
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4, $5, $6,
			$7, $8, $9, $10, $11, 'production', NULLIF($12, '')::uuid
		)
	`, in.WorkspaceID, in.SessionID, in.ArtifactID, newVersion, schemaName, schemaVersion,
		ArtifactCanonicalizationVersion, in.ContentHash, string(in.AccessLevel),
		in.GoalVersion, in.PlanVersion, in.ProducedByAttemptID); err != nil {
		return false, err
	}

	var watermark int64
	if err = tx.QueryRow(ctx, `
		SELECT research_artifact_policy_watermark_for_tx($1::uuid, $2::uuid)
	`, in.WorkspaceID, in.SessionID).Scan(&watermark); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET current_version = $4, eligibility_revision = $5,
		    provenance_completeness = 'complete'
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
		  AND current_version = $6 AND eligibility_revision = $7
	`, in.WorkspaceID, in.SessionID, in.ArtifactID, newVersion, newEligibility,
		currentVersion, oldEligibility)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, fmt.Errorf("%w: artifact current-version CAS failed", ErrInvalidTransition)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
			workspace_id, session_id, watermark, mutation_kind, artifact_id,
			old_eligibility_revision, new_eligibility_revision,
			old_current_version, new_current_version, old_access_level, new_access_level
		) VALUES (
			$1::uuid, $2::uuid, $3, 'current_version', $4::uuid, $5, $6, $7, $8,
			NULL, NULL
		)
	`, in.WorkspaceID, in.SessionID, watermark, in.ArtifactID,
		oldEligibility, newEligibility, currentVersion, newVersion)
	if err != nil {
		return false, err
	}
	return true, nil
}
