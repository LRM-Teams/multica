package researchrun

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// WithdrawArtifact removes one passport from future ordinary admission while
// retaining its immutable versions and domain history for authorized audit.
func (s *PostgresStore) WithdrawArtifact(ctx context.Context, in WithdrawArtifactInput) (receipt ArtifactWithdrawal, err error) {
	if err = in.validate(); err != nil {
		return ArtifactWithdrawal{}, err
	}
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.ArtifactID = strings.TrimSpace(in.ArtifactID)
	in.DecisionID = strings.TrimSpace(in.DecisionID)
	in.ActorID = strings.TrimSpace(in.ActorID)
	in.Reason = strings.TrimSpace(in.Reason)

	tx, err := s.beginResearchTx(ctx, txOpArtifactWithdraw, pgx.TxOptions{})
	if err != nil {
		return ArtifactWithdrawal{}, err
	}
	defer tx.Rollback(ctx)

	if _, err = loadRunForUpdate(ctx, tx, in.SessionID, in.WorkspaceID); err != nil {
		return ArtifactWithdrawal{}, err
	}
	if err = ensureSessionPolicyStateTx(ctx, tx, in.WorkspaceID, in.SessionID); err != nil {
		return ArtifactWithdrawal{}, fmt.Errorf("ensure withdrawal policy state: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		SELECT 1
		FROM research_artifact_policy_state
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		FOR UPDATE
	`, in.WorkspaceID, in.SessionID); err != nil {
		return ArtifactWithdrawal{}, fmt.Errorf("lock withdrawal policy state: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		SELECT id
		FROM research_artifact_policy_grant
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		ORDER BY id
		FOR UPDATE
	`, in.WorkspaceID, in.SessionID); err != nil {
		return ArtifactWithdrawal{}, fmt.Errorf("lock withdrawal policy grants: %w", err)
	}

	receipt.ArtifactID = in.ArtifactID
	receipt.NewLifecycle = ArtifactLifecycleWithdrawn
	err = tx.QueryRow(ctx, `
		SELECT entity_kind, lifecycle_status, eligibility_revision
		FROM research_artifact_passport
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
		FOR UPDATE
	`, in.WorkspaceID, in.SessionID, in.ArtifactID).Scan(
		&receipt.EntityKind, &receipt.OldLifecycle, &receipt.OldEligibilityRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ArtifactWithdrawal{}, fmt.Errorf("%w: artifact passport not found", ErrInvalidTransition)
	}
	if err != nil {
		return ArtifactWithdrawal{}, fmt.Errorf("lock withdrawal passport: %w", err)
	}
	if receipt.OldLifecycle == ArtifactLifecycleWithdrawn {
		var decisionID *string
		err = tx.QueryRow(ctx, `
			SELECT id::text, old_status, new_status,
			       old_eligibility_revision, new_eligibility_revision,
			       policy_watermark, decision_id::text
			FROM research_artifact_lifecycle_event
			WHERE workspace_id = $1::uuid AND session_id = $2::uuid
			  AND artifact_id = $3::uuid AND new_status = 'withdrawn'
			ORDER BY new_eligibility_revision DESC
			LIMIT 1
		`, in.WorkspaceID, in.SessionID, in.ArtifactID).Scan(
			&receipt.LifecycleEventID, &receipt.OldLifecycle, &receipt.NewLifecycle,
			&receipt.OldEligibilityRevision, &receipt.NewEligibilityRevision,
			&receipt.PolicyWatermark, &decisionID,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ArtifactWithdrawal{}, fmt.Errorf("%w: withdrawn passport has no lifecycle event", ErrInvalidTransition)
		}
		if err != nil {
			return ArtifactWithdrawal{}, fmt.Errorf("read existing withdrawal receipt: %w", err)
		}
		if decisionID != nil {
			receipt.DecisionID = *decisionID
		}
		if err = s.commitResearchTx(ctx, txOpArtifactWithdraw, tx); err != nil {
			return ArtifactWithdrawal{}, err
		}
		return receipt, nil
	}
	if _, err = ParseArtifactEntityKind(string(receipt.EntityKind)); err != nil {
		return ArtifactWithdrawal{}, err
	}
	if in.DecisionID != "" {
		var exists bool
		if err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM research_decision
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
			)
		`, in.WorkspaceID, in.SessionID, in.DecisionID).Scan(&exists); err != nil {
			return ArtifactWithdrawal{}, fmt.Errorf("read withdrawal decision: %w", err)
		}
		if !exists {
			return ArtifactWithdrawal{}, fmt.Errorf("%w: withdrawal decision not found", ErrInvalidTransition)
		}
		receipt.DecisionID = in.DecisionID
	}

	if err = tx.QueryRow(ctx, `
		SELECT research_artifact_policy_watermark_for_tx($1::uuid, $2::uuid)
	`, in.WorkspaceID, in.SessionID).Scan(&receipt.PolicyWatermark); err != nil {
		return ArtifactWithdrawal{}, fmt.Errorf("reserve withdrawal policy watermark: %w", err)
	}
	receipt.NewEligibilityRevision = receipt.OldEligibilityRevision + 1
	tag, err := tx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET lifecycle_status = 'withdrawn', eligibility_revision = $4
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
		  AND lifecycle_status = $5 AND eligibility_revision = $6
	`, in.WorkspaceID, in.SessionID, in.ArtifactID, receipt.NewEligibilityRevision,
		string(receipt.OldLifecycle), receipt.OldEligibilityRevision)
	if err != nil {
		return ArtifactWithdrawal{}, fmt.Errorf("withdraw artifact passport: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ArtifactWithdrawal{}, fmt.Errorf("%w: withdrawal passport changed", ErrInvalidTransition)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
			workspace_id, session_id, watermark, mutation_kind, artifact_id,
			old_eligibility_revision, new_eligibility_revision,
			old_lifecycle_status, new_lifecycle_status, eligibility_reason
		) VALUES ($1::uuid, $2::uuid, $3, 'lifecycle', $4::uuid, $5, $6, $7, 'withdrawn', $8)
	`, in.WorkspaceID, in.SessionID, receipt.PolicyWatermark, in.ArtifactID,
		receipt.OldEligibilityRevision, receipt.NewEligibilityRevision,
		string(receipt.OldLifecycle), in.Reason); err != nil {
		return ArtifactWithdrawal{}, fmt.Errorf("record withdrawal policy mutation: %w", err)
	}
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_artifact_lifecycle_event (
			workspace_id, session_id, artifact_id, old_status, new_status,
			old_eligibility_revision, new_eligibility_revision, policy_watermark,
			decision_id, actor_type, actor_id, reason
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4, 'withdrawn', $5, $6, $7,
			NULLIF($8, '')::uuid, $9, NULLIF($10, '')::uuid, $11
		)
		RETURNING id::text
	`, in.WorkspaceID, in.SessionID, in.ArtifactID, string(receipt.OldLifecycle),
		receipt.OldEligibilityRevision, receipt.NewEligibilityRevision, receipt.PolicyWatermark,
		in.DecisionID, in.ActorType, in.ActorID, in.Reason).Scan(&receipt.LifecycleEventID); err != nil {
		return ArtifactWithdrawal{}, fmt.Errorf("record withdrawal lifecycle event: %w", err)
	}

	if err = s.commitResearchTx(ctx, txOpArtifactWithdraw, tx); err != nil {
		return ArtifactWithdrawal{}, err
	}
	return receipt, nil
}
