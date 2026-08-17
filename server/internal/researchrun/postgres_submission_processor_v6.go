package researchrun

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ApplyReceivedV6Submissions(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	applied := 0
	for applied < limit {
		didApply, err := s.applyNextReceivedV6Submission(ctx)
		if err != nil {
			return applied, err
		}
		if !didApply {
			return applied, nil
		}
		applied++
	}
	return applied, nil
}

func (s *PostgresStore) applyNextReceivedV6Submission(ctx context.Context) (bool, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6SubmissionApply, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var submissionID, workspaceID, runID, kind, contentHash string
	var envelope json.RawMessage
	err = tx.QueryRow(ctx, `SELECT id::text,workspace_id::text,session_id::text,contract_kind,content_hash,envelope
		FROM research_v6_work_submission WHERE status='received' AND contract_kind IN ('atomic_result_submission','discussion_turn_submission','integration_submission')
		ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&submissionID, &workspaceID, &runID, &kind, &contentHash, &envelope)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = lockRunForMutation(ctx, tx, runID, workspaceID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='processing',updated_at=now() WHERE id=$1::uuid`, submissionID); err != nil {
		return false, err
	}
	applyTx, err := tx.Begin(ctx)
	if err != nil {
		return false, err
	}
	decoded := DecodedV6Contract{Kind: V6ContractKind(kind), Envelope: envelope, Canonical: envelope, ContentHash: contentHash}
	switch decoded.Kind {
	case V6ContractAtomicResultSubmission:
		_, err = s.acceptAtomicResultV6Tx(ctx, applyTx, submissionID, decoded)
	case V6ContractDiscussionTurnSubmission:
		err = s.applyDiscussionTurnV6Tx(ctx, applyTx, submissionID, decoded)
	case V6ContractIntegrationSubmission:
		_, err = s.applyIntegrationV6Tx(ctx, applyTx, submissionID, decoded)
	default:
		// Discussion and Integration are applied by their typed modules below;
		// retaining processing state here would hide work, so fail closed.
		err = ErrInvalidContract
	}
	if err != nil {
		_ = applyTx.Rollback(ctx)
		if !isTerminalV6SubmissionError(err) {
			return false, err
		}
		if _, updateErr := tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='rejected',
			outcome=jsonb_build_object('error',$2),updated_at=now() WHERE id=$1::uuid`, submissionID, err.Error()); updateErr != nil {
			return false, updateErr
		}
		if _, eventErr := appendEvent(ctx, tx, workspaceID, runID, "v6_work_submission_rejected", "v6-submission-rejected:"+submissionID,
			"system", "", map[string]any{"submission_id": submissionID, "contract_kind": kind, "reason": err.Error()}); eventErr != nil {
			return false, eventErr
		}
	} else if err = applyTx.Commit(ctx); err != nil {
		return false, err
	}
	if err = s.commitResearchTx(ctx, txOpV6SubmissionApply, tx); err != nil {
		return false, err
	}
	return true, nil
}

func isTerminalV6SubmissionError(err error) bool {
	return errors.Is(err, ErrInvalidContract) || errors.Is(err, ErrAttemptNotAssigned) ||
		errors.Is(err, ErrWorkItemChanged) || errors.Is(err, ErrV6NodeAlreadyAbsorbed) ||
		errors.Is(err, ErrV6InvalidTierTransition) || errors.Is(err, ErrResultConflict)
}
