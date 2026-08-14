package researchrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type researchTxOperation string

type researchTxFaultPoint string

const (
	txAfterBegin   researchTxFaultPoint = "after_begin"
	txBeforeCommit researchTxFaultPoint = "before_commit"
	txAfterCommit  researchTxFaultPoint = "after_commit"

	txResultAfterMethod            researchTxFaultPoint = "result_after_method"
	txResultAfterQuestion          researchTxFaultPoint = "result_after_question"
	txResultAfterTask              researchTxFaultPoint = "result_after_task"
	txResultAfterTaskDependency    researchTxFaultPoint = "result_after_task_dependency"
	txResultAfterSourceSnapshot    researchTxFaultPoint = "result_after_source_snapshot"
	txResultAfterLegacySource      researchTxFaultPoint = "result_after_legacy_source"
	txResultAfterObservation       researchTxFaultPoint = "result_after_observation"
	txResultAfterClaim             researchTxFaultPoint = "result_after_claim"
	txResultAfterEvidenceLink      researchTxFaultPoint = "result_after_evidence_link"
	txResultAfterReport            researchTxFaultPoint = "result_after_report"
	txResultAfterReportClaim       researchTxFaultPoint = "result_after_report_claim"
	txResultAfterEvaluation        researchTxFaultPoint = "result_after_evaluation_decision"
	txResultAfterAttemptTerminal   researchTxFaultPoint = "result_after_attempt_terminal"
	txResultAfterCircuitSettlement researchTxFaultPoint = "result_after_circuit_settlement"
	txResultAfterResultArtifact    researchTxFaultPoint = "result_after_result_artifact"
	txResultAfterArtifactLineage   researchTxFaultPoint = "result_after_artifact_lineage"
	txResultAfterTaskTerminal      researchTxFaultPoint = "result_after_task_terminal"
	txResultAfterRunUpdate         researchTxFaultPoint = "result_after_run_update"
	txResultAfterEvent             researchTxFaultPoint = "result_after_event"
)

const (
	txOpRunCreate                 researchTxOperation = "run.create"
	txOpRunInitialize             researchTxOperation = "run.initialize"
	txOpTaskActivateReady         researchTxOperation = "task.activate_ready"
	txOpDispatchIntentCreate      researchTxOperation = "dispatch_intent.create"
	txOpDispatchIntentClaim       researchTxOperation = "dispatch_intent.claim"
	txOpDispatchIntentReschedule  researchTxOperation = "dispatch_intent.reschedule"
	txOpDispatchIntentFail        researchTxOperation = "dispatch_intent.fail"
	txOpDispatchIntentAcknowledge researchTxOperation = "dispatch_intent.acknowledge"
	txOpAttemptAttachInbox        researchTxOperation = "attempt.attach_inbox"
	txOpAttemptFail               researchTxOperation = "attempt.fail"
	txOpAttemptCancelRequest      researchTxOperation = "attempt.cancel_request"
	txOpAttemptCancelComplete     researchTxOperation = "attempt.cancel_complete"
	txOpResultAccept              researchTxOperation = "result.accept"
	txOpControlTaskCreate         researchTxOperation = "control_task.create"
	txOpRunAwaitConfirmation      researchTxOperation = "run.await_confirmation"
	txOpRunComplete               researchTxOperation = "run.complete"
	txOpRunResume                 researchTxOperation = "run.resume"
	txOpRunTransition             researchTxOperation = "run.transition"
	txOpRunSteer                  researchTxOperation = "run.steer"
	txOpNodeCommand               researchTxOperation = "node_command.execute"
	txOpCircuitFailure            researchTxOperation = "circuit.record_failure"
	txOpCircuitSuccess            researchTxOperation = "circuit.record_success"
	txOpCircuitProbeClaim         researchTxOperation = "circuit.probe_claim"
	txOpCircuitProbeResolve       researchTxOperation = "circuit.probe_resolve"
	txOpExecutionTargetDefer      researchTxOperation = "execution_target.defer"
	txOpBudgetExhausted           researchTxOperation = "budget.exhausted"
	txOpAttemptRuntimeReconcile   researchTxOperation = "attempt.runtime_reconcile"
	txOpProjectionAcknowledge     researchTxOperation = "projection.acknowledge"
	txOpProjectionRetry           researchTxOperation = "projection.retry"
	txOpArtifactLifecycleChange   researchTxOperation = "artifact.lifecycle_change"
	txOpReconcileLeaseClaim       researchTxOperation = "reconcile_lease.claim"
	txOpReconcileLeaseRenew       researchTxOperation = "reconcile_lease.renew"
	txOpReconcileLeaseRelease     researchTxOperation = "reconcile_lease.release"
	txOpInquiryTransition         researchTxOperation = "inquiry.transition"
	txOpSearchLineageRecord       researchTxOperation = "search_lineage.record"
	txOpArtifactSupersede         researchTxOperation = "artifact.supersede"
	txOpArtifactWithdraw          researchTxOperation = "artifact.withdraw"
	txOpInquiryGraphCreate        researchTxOperation = "inquiry_graph.create"
	txOpInquiryStatusUpdate       researchTxOperation = "inquiry_status.update"
	txOpStrategyPromotion         researchTxOperation = "strategy.promote"
	txOpTaskInquiryTargetsBind    researchTxOperation = "task_inquiry_targets.bind"
)

type researchTxFaultHook func(context.Context, researchTxOperation, researchTxFaultPoint) error

func resultAcceptanceSemanticFaultPoints() []researchTxFaultPoint {
	return []researchTxFaultPoint{
		txResultAfterMethod,
		txResultAfterQuestion,
		txResultAfterTask,
		txResultAfterTaskDependency,
		txResultAfterSourceSnapshot,
		txResultAfterLegacySource,
		txResultAfterObservation,
		txResultAfterClaim,
		txResultAfterEvidenceLink,
		txResultAfterReport,
		txResultAfterReportClaim,
		txResultAfterEvaluation,
		txResultAfterAttemptTerminal,
		txResultAfterCircuitSettlement,
		txResultAfterResultArtifact,
		txResultAfterArtifactLineage,
		txResultAfterTaskTerminal,
		txResultAfterRunUpdate,
		txResultAfterEvent,
	}
}

type researchTxBeginFunc func(context.Context, pgx.TxOptions) (pgx.Tx, error)

var ErrCommitOutcomeUnknown = errors.New("research transaction commit outcome unknown")

func beginResearchTx(
	ctx context.Context,
	operation researchTxOperation,
	options pgx.TxOptions,
	begin researchTxBeginFunc,
	hook researchTxFaultHook,
) (pgx.Tx, error) {
	tx, err := begin(ctx, options)
	if err != nil {
		return nil, err
	}
	if hook == nil {
		return tx, nil
	}
	if err = hook(ctx, operation, txAfterBegin); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback research transaction after begin fault: %w", rollbackErr))
		}
		return nil, err
	}
	return tx, nil
}

func commitResearchTx(
	ctx context.Context,
	operation researchTxOperation,
	tx pgx.Tx,
	hook researchTxFaultHook,
) error {
	if hook != nil {
		if err := hook(ctx, operation, txBeforeCommit); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if hook != nil {
		if err := hook(ctx, operation, txAfterCommit); err != nil {
			return fmt.Errorf("%w: %w", ErrCommitOutcomeUnknown, err)
		}
	}
	return nil
}

func (s *PostgresStore) beginResearchTx(
	ctx context.Context,
	operation researchTxOperation,
	options pgx.TxOptions,
) (pgx.Tx, error) {
	return beginResearchTx(ctx, operation, options, s.pool.BeginTx, s.txFaultHook)
}

func (s *PostgresStore) commitResearchTx(
	ctx context.Context,
	operation researchTxOperation,
	tx pgx.Tx,
) error {
	return commitResearchTx(ctx, operation, tx, s.txFaultHook)
}
