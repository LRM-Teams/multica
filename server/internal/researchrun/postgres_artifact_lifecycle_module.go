package researchrun

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

var _ artifactLifecycleStore = (*PostgresStore)(nil)

func (s *PostgresStore) ApplyArtifactLifecycleChange(ctx context.Context, change artifactLifecycleChange) (artifactLifecycleOutcome, error) {
	tx, err := s.beginResearchTx(ctx, txOpArtifactLifecycleChange, pgx.TxOptions{})
	if err != nil {
		return artifactLifecycleOutcome{}, err
	}
	defer tx.Rollback(ctx)

	if err = lockRunForMutation(ctx, tx, change.SessionID, change.WorkspaceID); err != nil {
		return artifactLifecycleOutcome{}, err
	}
	ids := []string{change.ArtifactID}
	if change.SuccessorArtifactID != "" {
		ids = append(ids, change.SuccessorArtifactID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err = lockArtifactPassportRowTx(ctx, tx, change.WorkspaceID, change.SessionID, id); err != nil {
			return artifactLifecycleOutcome{}, err
		}
	}

	replayed, outcome, err := loadArtifactLifecycleReplayTx(ctx, tx, change)
	if err != nil {
		return artifactLifecycleOutcome{}, err
	}
	if replayed {
		if err = s.commitResearchTx(ctx, txOpArtifactLifecycleChange, tx); err != nil {
			return artifactLifecycleOutcome{}, err
		}
		outcome.Replayed = true
		return outcome, nil
	}

	var oldLifecycle string
	var currentVersion int32
	var oldRevision int64
	if err = tx.QueryRow(ctx, `
		SELECT lifecycle_status, current_version, eligibility_revision
		FROM research_artifact_passport
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
	`, change.WorkspaceID, change.SessionID, change.ArtifactID).Scan(&oldLifecycle, &currentVersion, &oldRevision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return artifactLifecycleOutcome{}, ErrRunNotFound
		}
		return artifactLifecycleOutcome{}, err
	}
	if oldLifecycle != string(ArtifactLifecycleRegistered) && oldLifecycle != string(ArtifactLifecycleAccepted) {
		return artifactLifecycleOutcome{}, fmt.Errorf("%w: artifact lifecycle is %s", ErrInvalidTransition, oldLifecycle)
	}

	var watermark int64
	if err = tx.QueryRow(ctx, `SELECT research_artifact_policy_watermark_for_tx($1::uuid,$2::uuid)`, change.WorkspaceID, change.SessionID).Scan(&watermark); err != nil {
		return artifactLifecycleOutcome{}, err
	}
	newRevision := oldRevision + 1
	target := ArtifactLifecycleWithdrawn
	mutationKind := "lifecycle"
	if change.Kind == artifactLifecycleSupersede {
		target = ArtifactLifecycleSuperseded
		mutationKind = "supersession"
	}
	tag, err := tx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET lifecycle_status=$4, eligibility_revision=$5
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		  AND lifecycle_status=$6 AND eligibility_revision=$7 AND current_version=$8
	`, change.WorkspaceID, change.SessionID, change.ArtifactID, target, newRevision, oldLifecycle, oldRevision, currentVersion)
	if err != nil {
		return artifactLifecycleOutcome{}, err
	}
	if tag.RowsAffected() != 1 {
		return artifactLifecycleOutcome{}, fmt.Errorf("%w: artifact lifecycle changed concurrently", ErrInvalidTransition)
	}
	if change.Kind == artifactLifecycleWithdraw {
		if err = insertArtifactWithdrawalLedgerTx(ctx, tx, change, oldLifecycle, oldRevision, newRevision, watermark); err != nil {
			return artifactLifecycleOutcome{}, err
		}
	} else if err = insertArtifactSupersessionLedgerTx(ctx, tx, change, oldRevision, newRevision, watermark); err != nil {
		return artifactLifecycleOutcome{}, err
	}
	newLifecycle := ""
	if change.Kind == artifactLifecycleWithdraw {
		newLifecycle = string(target)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id,session_id,watermark,mutation_kind,artifact_id,
		  old_eligibility_revision,new_eligibility_revision,
		  old_lifecycle_status,new_lifecycle_status
		) VALUES ($1::uuid,$2::uuid,$3,$4,$5::uuid,$6,$7,$8,NULLIF($9,''))
	`, change.WorkspaceID, change.SessionID, watermark, mutationKind, change.ArtifactID,
		oldRevision, newRevision, oldLifecycle, newLifecycle); err != nil {
		return artifactLifecycleOutcome{}, err
	}
	if err = s.commitResearchTx(ctx, txOpArtifactLifecycleChange, tx); err != nil {
		return artifactLifecycleOutcome{}, err
	}
	return artifactLifecycleOutcome{ArtifactID: change.ArtifactID, Lifecycle: target, EligibilityRevision: newRevision, PolicyWatermark: watermark}, nil
}

func insertArtifactWithdrawalLedgerTx(ctx context.Context, tx pgx.Tx, change artifactLifecycleChange, oldLifecycle string, oldRevision, newRevision, watermark int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_lifecycle_event (
		  id,workspace_id,session_id,artifact_id,old_status,new_status,
		  old_eligibility_revision,new_eligibility_revision,policy_watermark,
		  actor_type,actor_id,reason
		) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'withdrawn',$6,$7,$8,$9,NULLIF($10,'')::uuid,$11)
	`, change.OperationID, change.WorkspaceID, change.SessionID, change.ArtifactID, oldLifecycle,
		oldRevision, newRevision, watermark, change.ActorType, change.ActorID, change.Reason)
	return err
}

func insertArtifactSupersessionLedgerTx(ctx context.Context, tx pgx.Tx, change artifactLifecycleChange, oldRevision, newRevision, watermark int64) error {
	var oldVersionID, successorVersionID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM research_artifact_version
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid AND version=(
		  SELECT current_version FROM research_artifact_passport
		  WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)
	`, change.WorkspaceID, change.SessionID, change.ArtifactID).Scan(&oldVersionID); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM research_artifact_version
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid AND version=(
		  SELECT current_version FROM research_artifact_passport
		  WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)
	`, change.WorkspaceID, change.SessionID, change.SuccessorArtifactID).Scan(&successorVersionID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_supersession (
		  id,workspace_id,session_id,successor_version_id,superseded_version_id,
		  superseded_artifact_id,reason,decision_id,policy_watermark,
		  old_eligibility_revision,new_eligibility_revision
		) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8::uuid,$9,$10,$11)
	`, change.OperationID, change.WorkspaceID, change.SessionID, successorVersionID, oldVersionID,
		change.ArtifactID, change.Reason, change.DecisionID, watermark, oldRevision, newRevision)
	return err
}

func loadArtifactLifecycleReplayTx(ctx context.Context, tx pgx.Tx, change artifactLifecycleChange) (bool, artifactLifecycleOutcome, error) {
	var artifactID, lifecycle string
	var revision, watermark int64
	query := `
		SELECT event.artifact_id::text, passport.lifecycle_status, passport.eligibility_revision, event.policy_watermark
		FROM research_artifact_lifecycle_event event
		JOIN research_artifact_passport passport
		  ON passport.workspace_id=event.workspace_id AND passport.session_id=event.session_id AND passport.id=event.artifact_id
		WHERE event.workspace_id=$1::uuid AND event.session_id=$2::uuid AND event.id=$3::uuid
		  AND event.artifact_id=$4::uuid AND event.new_status='withdrawn' AND event.reason=$5`
	args := []any{change.WorkspaceID, change.SessionID, change.OperationID, change.ArtifactID, change.Reason}
	if change.Kind == artifactLifecycleSupersede {
		query = `
			SELECT edge.superseded_artifact_id::text, passport.lifecycle_status, passport.eligibility_revision, edge.policy_watermark
			FROM research_artifact_supersession edge
			JOIN research_artifact_version successor ON successor.id=edge.successor_version_id
			JOIN research_artifact_passport passport
			  ON passport.workspace_id=edge.workspace_id AND passport.session_id=edge.session_id AND passport.id=edge.superseded_artifact_id
			WHERE edge.workspace_id=$1::uuid AND edge.session_id=$2::uuid AND edge.id=$3::uuid
			  AND edge.superseded_artifact_id=$4::uuid AND successor.artifact_id=$5::uuid
			  AND edge.decision_id=$6::uuid AND edge.reason=$7`
		args = []any{change.WorkspaceID, change.SessionID, change.OperationID, change.ArtifactID, change.SuccessorArtifactID, change.DecisionID, change.Reason}
	}
	err := tx.QueryRow(ctx, query, args...).Scan(&artifactID, &lifecycle, &revision, &watermark)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, artifactLifecycleOutcome{}, nil
	}
	if err != nil {
		return false, artifactLifecycleOutcome{}, err
	}
	return true, artifactLifecycleOutcome{ArtifactID: artifactID, Lifecycle: ArtifactLifecycleStatus(lifecycle), EligibilityRevision: revision, PolicyWatermark: watermark}, nil
}
