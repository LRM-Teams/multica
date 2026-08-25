package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const v6SubmissionApplyFailureLimit = 3

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
		FROM research_v6_work_submission WHERE status='received'
		AND COALESCE((outcome->>'next_apply_after')::timestamptz,'-infinity'::timestamptz)<=now()
		AND contract_kind IN ('atomic_result_submission','discussion_turn_submission','integration_submission')
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
	if err == nil {
		// Force deferred provenance and reciprocal guards to run inside the
		// savepoint. A deterministic constraint error can then be rolled back and
		// recorded without poisoning the global received-submission queue.
		_, err = applyTx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`)
	}
	if err != nil {
		_ = applyTx.Rollback(ctx)
		if !isTerminalV6SubmissionError(err) {
			diagnostic := v6SubmissionApplyDiagnostic(err)
			slog.Warn("research V6 submission canonical apply failed", "submission_id", submissionID,
				"workspace_id", workspaceID, "run_id", runID, "contract_kind", kind, "error", err)
			terminal, retryErr := s.recordV6SubmissionApplyFailureTx(ctx, tx, workspaceID, runID, submissionID, diagnostic)
			if retryErr != nil {
				return false, retryErr
			}
			if terminal {
				workItemID, requeued, requeueErr := s.requeueV6RejectedWorkTx(ctx, tx, workspaceID, runID, submissionID, diagnostic, "platform_error")
				if requeueErr != nil {
					return false, requeueErr
				}
				if _, eventErr := appendEvent(ctx, tx, workspaceID, runID, "v6_work_submission_rejected", "v6-submission-rejected:"+submissionID,
					"system", "", map[string]any{"submission_id": submissionID, "contract_kind": kind, "reason": diagnostic,
						"work_item_id": workItemID, "work_requeued": requeued, "failure_class": "platform_error"}); eventErr != nil {
					return false, eventErr
				}
			}
			if err = s.commitResearchTx(ctx, txOpV6SubmissionApply, tx); err != nil {
				return false, err
			}
			return true, nil
		}
		if _, updateErr := tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='rejected',
			outcome=jsonb_build_object('error',$4::text),updated_at=now()
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, runID, submissionID, err.Error()); updateErr != nil {
			return false, updateErr
		}
		eventPayload := map[string]any{"submission_id": submissionID, "contract_kind": kind, "reason": err.Error()}
		if errors.Is(err, ErrWorkItemChanged) {
			workItemID, requeued, requeueErr := s.requeueV6RejectedWorkTx(ctx, tx, workspaceID, runID, submissionID, err.Error(), "contract_rejected")
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

func v6SubmissionApplyDiagnostic(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.ConstraintName != "" {
			return fmt.Sprintf("canonical persistence failed (%s, constraint %s)", pgErr.Code, pgErr.ConstraintName)
		}
		return fmt.Sprintf("canonical persistence failed (%s)", pgErr.Code)
	}
	return "canonical persistence failed"
}

func (s *PostgresStore) recordV6SubmissionApplyFailureTx(ctx context.Context, tx pgx.Tx, workspaceID, runID, submissionID, diagnostic string) (bool, error) {
	var failures int
	var status string
	err := tx.QueryRow(ctx, `UPDATE research_v6_work_submission
		SET outcome=outcome || jsonb_build_object(
			'apply_failure_count',COALESCE((outcome->>'apply_failure_count')::int,0)+1,
			'last_error',$4::text,
			'next_apply_after',CASE
				WHEN COALESCE((outcome->>'apply_failure_count')::int,0)+1 >= $5::int THEN NULL
				ELSE to_jsonb(now()+interval '15 seconds') END),
		status=CASE WHEN COALESCE((outcome->>'apply_failure_count')::int,0)+1 >= $5::int THEN 'rejected' ELSE 'received' END,
		updated_at=now()
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		RETURNING (outcome->>'apply_failure_count')::int,status`, workspaceID, runID, submissionID, diagnostic, v6SubmissionApplyFailureLimit).Scan(&failures, &status)
	if err != nil {
		return false, err
	}
	return failures >= v6SubmissionApplyFailureLimit && status == "rejected", nil
}

func isTerminalV6SubmissionError(err error) bool {
	return errors.Is(err, ErrInvalidContract) || errors.Is(err, ErrAttemptNotAssigned) ||
		errors.Is(err, ErrWorkItemChanged) || errors.Is(err, ErrV6NodeAlreadyAbsorbed) ||
		errors.Is(err, ErrV6InvalidTierTransition) || errors.Is(err, ErrResultConflict)
}

// requeueV6RejectedWorkTx settles a rejected submission immediately instead
// of leaving its attempt and team membership active until lease expiry. The
// consumed attempt is refunded for optimistic-concurrency and platform
// failures, but the attempt-number ceiling still terminates a systematic loop.
func (s *PostgresStore) requeueV6RejectedWorkTx(ctx context.Context, tx pgx.Tx, workspaceID, runID, submissionID, reason, failureClass string) (string, bool, error) {
	var workItemID, membershipID string
	var attemptNumber int
	err := tx.QueryRow(ctx, `UPDATE research_work_item_attempt a
		SET status='failed',failure_class=$5,diagnostics=$4,completed_at=now(),updated_at=now()
		FROM research_v6_work_submission s
		WHERE s.workspace_id=$1::uuid AND s.session_id=$2::uuid AND s.id=$3::uuid
		  AND (a.workspace_id,a.session_id,a.id)=(s.workspace_id,s.session_id,s.attempt_id)
		  AND a.status IN ('dispatching','running')
		RETURNING a.work_item_id::text,a.membership_id::text,a.attempt_number`, workspaceID, runID, submissionID, reason, failureClass).Scan(&workItemID, &membershipID, &attemptNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var attemptCount, maxAttempts int
	err = tx.QueryRow(ctx, `SELECT attempt_count,max_attempts FROM research_work_item
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		  AND status IN ('dispatching','running','awaiting_input') FOR UPDATE`, workspaceID, runID, workItemID).Scan(&attemptCount, &maxAttempts)
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
		SET status=$4,attempt_count=$5,terminal_reason_code=$6,terminal_reason_detail=CASE WHEN $6='' THEN terminal_reason_detail ELSE $7 END,
		    ready_at=CASE WHEN $4='ready' THEN now() ELSE ready_at END,
		    lease_token=NULL,lease_expires_at=NULL,state_version=state_version+1,updated_at=now()
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, runID, workItemID, status, attemptCount, terminalReason, reason); err != nil {
		return "", false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_team_membership m SET state='idle'
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.id=$3::uuid AND m.state='working'
		AND NOT EXISTS (SELECT 1 FROM research_work_item_attempt active
			WHERE active.workspace_id=m.workspace_id AND active.session_id=m.session_id
			  AND active.membership_id=m.id AND active.status IN ('dispatching','running'))`, workspaceID, runID, membershipID); err != nil {
		return "", false, err
	}
	return workItemID, status == "ready", nil
}
