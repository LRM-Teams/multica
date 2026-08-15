package researchrun

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

type supersessionEndpoint struct {
	versionID           string
	artifactID          string
	version             int32
	currentVersion      int32
	eligibilityRevision int64
	lifecycle           ArtifactLifecycleStatus
}

// SupersedeArtifact records immutable, Decision-backed version lineage and
// removes the superseded passport from future ordinary admission.
func (s *PostgresStore) SupersedeArtifact(ctx context.Context, in SupersedeArtifactInput) (receipt ArtifactSupersession, err error) {
	if err = in.validate(); err != nil {
		return ArtifactSupersession{}, err
	}
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.SuccessorVersionID = strings.TrimSpace(in.SuccessorVersionID)
	in.SupersededVersionID = strings.TrimSpace(in.SupersededVersionID)
	in.DecisionID = strings.TrimSpace(in.DecisionID)
	in.Reason = strings.TrimSpace(in.Reason)

	tx, err := s.beginResearchTx(ctx, txOpArtifactSupersede, pgx.TxOptions{})
	if err != nil {
		return ArtifactSupersession{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = loadRunForUpdate(ctx, tx, in.SessionID, in.WorkspaceID); err != nil {
		return ArtifactSupersession{}, err
	}
	if err = ensureSessionPolicyStateTx(ctx, tx, in.WorkspaceID, in.SessionID); err != nil {
		return ArtifactSupersession{}, fmt.Errorf("ensure supersession policy state: %w", err)
	}
	if _, err = tx.Exec(ctx, `SELECT 1 FROM research_artifact_policy_state WHERE workspace_id=$1::uuid AND session_id=$2::uuid FOR UPDATE`, in.WorkspaceID, in.SessionID); err != nil {
		return ArtifactSupersession{}, fmt.Errorf("lock supersession policy state: %w", err)
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM research_artifact_policy_grant WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY id FOR UPDATE`, in.WorkspaceID, in.SessionID); err != nil {
		return ArtifactSupersession{}, fmt.Errorf("lock supersession policy grants: %w", err)
	}

	endpoints := map[string]*supersessionEndpoint{
		in.SuccessorVersionID:  {versionID: in.SuccessorVersionID},
		in.SupersededVersionID: {versionID: in.SupersededVersionID},
	}
	for _, endpoint := range endpoints {
		err = tx.QueryRow(ctx, `
			SELECT v.artifact_id::text, v.version, p.current_version,
			       p.eligibility_revision, p.lifecycle_status
			FROM research_artifact_version v
			JOIN research_artifact_passport p
			  ON p.workspace_id=v.workspace_id AND p.session_id=v.session_id AND p.id=v.artifact_id
			WHERE v.workspace_id=$1::uuid AND v.session_id=$2::uuid AND v.id=$3::uuid
		`, in.WorkspaceID, in.SessionID, endpoint.versionID).Scan(
			&endpoint.artifactID, &endpoint.version, &endpoint.currentVersion,
			&endpoint.eligibilityRevision, &endpoint.lifecycle,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ArtifactSupersession{}, fmt.Errorf("%w: supersession version not found", ErrInvalidTransition)
		}
		if err != nil {
			return ArtifactSupersession{}, fmt.Errorf("read supersession endpoint: %w", err)
		}
	}
	artifactIDs := []string{endpoints[in.SuccessorVersionID].artifactID, endpoints[in.SupersededVersionID].artifactID}
	sort.Strings(artifactIDs)
	if artifactIDs[0] == artifactIDs[1] {
		artifactIDs = artifactIDs[:1]
	}
	for _, artifactID := range artifactIDs {
		if _, err = tx.Exec(ctx, `
			SELECT 1 FROM research_artifact_passport
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE
		`, in.WorkspaceID, in.SessionID, artifactID); err != nil {
			return ArtifactSupersession{}, fmt.Errorf("lock supersession passport: %w", err)
		}
	}
	// Re-read locked endpoint facts; the discovery read above only established
	// deterministic passport order and is never used as mutation authority.
	for _, endpoint := range endpoints {
		if err = tx.QueryRow(ctx, `
			SELECT v.version, p.current_version, p.eligibility_revision, p.lifecycle_status
			FROM research_artifact_version v
			JOIN research_artifact_passport p
			  ON p.workspace_id=v.workspace_id AND p.session_id=v.session_id AND p.id=v.artifact_id
			WHERE v.workspace_id=$1::uuid AND v.session_id=$2::uuid AND v.id=$3::uuid
		`, in.WorkspaceID, in.SessionID, endpoint.versionID).Scan(
			&endpoint.version, &endpoint.currentVersion, &endpoint.eligibilityRevision, &endpoint.lifecycle,
		); err != nil {
			return ArtifactSupersession{}, fmt.Errorf("re-read locked supersession endpoint: %w", err)
		}
	}

	receipt.SuccessorVersionID = in.SuccessorVersionID
	receipt.SupersededVersionID = in.SupersededVersionID
	receipt.SupersededArtifactID = endpoints[in.SupersededVersionID].artifactID
	receipt.DecisionID = in.DecisionID
	receipt.Reason = in.Reason
	var existingDecision, existingReason string
	err = tx.QueryRow(ctx, `
		SELECT id::text, old_eligibility_revision, new_eligibility_revision,
		       policy_watermark, decision_id::text, reason
		FROM research_artifact_supersession
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		  AND successor_version_id=$3::uuid AND superseded_version_id=$4::uuid
	`, in.WorkspaceID, in.SessionID, in.SuccessorVersionID, in.SupersededVersionID).Scan(
		&receipt.ID, &receipt.OldEligibilityRevision, &receipt.NewEligibilityRevision,
		&receipt.PolicyWatermark, &existingDecision, &existingReason,
	)
	if err == nil {
		if existingDecision != in.DecisionID || existingReason != in.Reason {
			return ArtifactSupersession{}, fmt.Errorf("%w: supersession replay facts differ", ErrInvalidTransition)
		}
		if err = s.commitResearchTx(ctx, txOpArtifactSupersede, tx); err != nil {
			return ArtifactSupersession{}, err
		}
		return receipt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ArtifactSupersession{}, fmt.Errorf("read existing supersession: %w", err)
	}
	successor := endpoints[in.SuccessorVersionID]
	superseded := endpoints[in.SupersededVersionID]
	if successor.currentVersion != successor.version || (successor.lifecycle != ArtifactLifecycleRegistered && successor.lifecycle != ArtifactLifecycleAccepted) {
		return ArtifactSupersession{}, fmt.Errorf("%w: successor version is not currently admissible", ErrInvalidTransition)
	}
	if superseded.currentVersion != superseded.version || (superseded.lifecycle != ArtifactLifecycleRegistered && superseded.lifecycle != ArtifactLifecycleAccepted) {
		return ArtifactSupersession{}, fmt.Errorf("%w: superseded version is not currently admissible", ErrInvalidTransition)
	}
	var decisionExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM research_decision WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`, in.WorkspaceID, in.SessionID, in.DecisionID).Scan(&decisionExists); err != nil {
		return ArtifactSupersession{}, fmt.Errorf("read supersession decision: %w", err)
	}
	if !decisionExists {
		return ArtifactSupersession{}, fmt.Errorf("%w: supersession decision not found", ErrInvalidTransition)
	}
	var cyclic bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS (
		  WITH RECURSIVE walk(version_id) AS (
		    SELECT $1::uuid
		    UNION
		    SELECT edge.superseded_version_id
		    FROM walk
		    JOIN research_artifact_supersession edge
		      ON edge.workspace_id=$3::uuid AND edge.session_id=$4::uuid
		     AND edge.successor_version_id=walk.version_id
		  )
		  SELECT 1 FROM walk WHERE version_id=$2::uuid
		)
	`, in.SupersededVersionID, in.SuccessorVersionID, in.WorkspaceID, in.SessionID).Scan(&cyclic); err != nil {
		return ArtifactSupersession{}, fmt.Errorf("check supersession cycle: %w", err)
	}
	if cyclic {
		return ArtifactSupersession{}, fmt.Errorf("%w: supersession would create a cycle", ErrInvalidTransition)
	}
	if err = tx.QueryRow(ctx, `SELECT research_artifact_policy_watermark_for_tx($1::uuid,$2::uuid)`, in.WorkspaceID, in.SessionID).Scan(&receipt.PolicyWatermark); err != nil {
		return ArtifactSupersession{}, fmt.Errorf("reserve supersession watermark: %w", err)
	}
	receipt.OldEligibilityRevision = superseded.eligibilityRevision
	receipt.NewEligibilityRevision = superseded.eligibilityRevision + 1
	tag, err := tx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET lifecycle_status='superseded', eligibility_revision=$4
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		  AND current_version=$5 AND eligibility_revision=$6 AND lifecycle_status=$7
	`, in.WorkspaceID, in.SessionID, superseded.artifactID, receipt.NewEligibilityRevision,
		superseded.version, superseded.eligibilityRevision, string(superseded.lifecycle))
	if err != nil {
		return ArtifactSupersession{}, fmt.Errorf("supersede artifact passport: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ArtifactSupersession{}, fmt.Errorf("%w: superseded passport changed", ErrInvalidTransition)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		 workspace_id,session_id,watermark,mutation_kind,artifact_id,
		 old_eligibility_revision,new_eligibility_revision,eligibility_reason
		) VALUES ($1::uuid,$2::uuid,$3,'supersession',$4::uuid,$5,$6,$7)
	`, in.WorkspaceID, in.SessionID, receipt.PolicyWatermark, superseded.artifactID,
		receipt.OldEligibilityRevision, receipt.NewEligibilityRevision, in.Reason); err != nil {
		return ArtifactSupersession{}, fmt.Errorf("record supersession policy mutation: %w", err)
	}
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_artifact_supersession (
		 workspace_id,session_id,successor_version_id,superseded_version_id,superseded_artifact_id,
		 reason,decision_id,policy_watermark,old_eligibility_revision,new_eligibility_revision
		) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7::uuid,$8,$9,$10)
		RETURNING id::text
	`, in.WorkspaceID, in.SessionID, in.SuccessorVersionID, in.SupersededVersionID,
		superseded.artifactID, in.Reason, in.DecisionID, receipt.PolicyWatermark,
		receipt.OldEligibilityRevision, receipt.NewEligibilityRevision).Scan(&receipt.ID); err != nil {
		return ArtifactSupersession{}, fmt.Errorf("record artifact supersession: %w", err)
	}
	if err = s.commitResearchTx(ctx, txOpArtifactSupersede, tx); err != nil {
		return ArtifactSupersession{}, err
	}
	return receipt, nil
}
