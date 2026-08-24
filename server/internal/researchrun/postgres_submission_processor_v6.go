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
			outcome=jsonb_build_object('error',$2::text),updated_at=now() WHERE id=$1::uuid`, submissionID, err.Error()); updateErr != nil {
			return false, updateErr
		}
		eventPayload := map[string]any{"submission_id": submissionID, "contract_kind": kind, "reason": err.Error()}
		if errors.Is(err, ErrWorkItemChanged) {
			workItemID, requeued, requeueErr := s.requeueV6ConflictRejectedWorkTx(ctx, tx, submissionID, err.Error())
			if requeueErr != nil {
				return false, requeueErr
			}
			if workItemID != "" {
				eventPayload["work_item_id"] = workItemID
				eventPayload["work_requeued"] = requeued
			}
		}
		if _, eventErr := appendEvent(ctx, tx, workspaceID, runID, "v6_work_submission_rejected", "v6-submission-rejected:"+submissionID,
			"system", "", eventPayload); eventErr != nil {
			return false, eventErr
		}
	} else if err = commitResearchTx(ctx, txOpV6SubmissionApply, applyTx, nil); err != nil {
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

// requeueV6ConflictRejectedWorkTx settles an optimistic-concurrency rejection
// immediately instead of leaving the attempt to die of lease expiry. A
// conflict means the Work Item premise moved while the attempt was in flight
// — not that the Agent failed — so the attempt is failed as contract_rejected,
// the Work Item returns to the dispatch queue for a freshly compiled
// manifest, and the consumed attempt is refunded. Refunds stop once total
// attempts pass 4x max_attempts: a conflict that persistent is systematic and
// the Work Item must terminate instead of dispatching forever.
func (s *PostgresStore) requeueV6ConflictRejectedWorkTx(ctx context.Context, tx pgx.Tx, submissionID, reason string) (string, bool, error) {
	var workItemID string
	var attemptNumber int
	err := tx.QueryRow(ctx, `UPDATE research_work_item_attempt a
		SET status='failed',failure_class='contract_rejected',diagnostics=$2,completed_at=now(),updated_at=now()
		FROM research_v6_work_submission s
		WHERE s.id=$1::uuid AND a.id=s.attempt_id AND a.status IN ('dispatching','running')
		RETURNING a.work_item_id::text,a.attempt_number`, submissionID, reason).Scan(&workItemID, &attemptNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var attemptCount, maxAttempts int
	err = tx.QueryRow(ctx, `SELECT attempt_count,max_attempts FROM research_work_item
		WHERE id=$1::uuid AND status IN ('dispatching','running','awaiting_input') FOR UPDATE`, workItemID).Scan(&attemptCount, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return workItemID, false, nil
	}
	if err != nil {
		return "", false, err
	}
	if attemptNumber <= maxAttempts*4 && attemptCount > 0 {
		attemptCount--
	}
	status, terminalReason := "ready", ""
	if attemptCount >= maxAttempts {
		status, terminalReason = "failed", "attempt_budget_exhausted"
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item
		SET status=$2,attempt_count=$3,terminal_reason_code=$4,terminal_reason_detail=CASE WHEN $4='' THEN terminal_reason_detail ELSE $5 END,
		    ready_at=CASE WHEN $2='ready' THEN now() ELSE ready_at END,
		    lease_token=NULL,lease_expires_at=NULL,state_version=state_version+1,updated_at=now()
		WHERE id=$1::uuid`, workItemID, status, attemptCount, terminalReason, reason); err != nil {
		return "", false, err
	}
	return workItemID, status == "ready", nil
}
