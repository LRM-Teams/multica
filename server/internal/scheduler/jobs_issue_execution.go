package scheduler

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
)

const JobNameIssueExecutionReconcile = "issue_execution_reconcile"

// IssueExecutionReconcileJob is both the outbox crash-recovery loop and the
// compatibility scanner for direct/legacy Issue writers. Reconciliation is
// idempotent under the Issue row lock and active-claim uniqueness fence.
func IssueExecutionReconcileJob(execution *service.IssueExecutionService) JobSpec {
	return JobSpec{
		Name:              JobNameIssueExecutionReconcile,
		Cadence:           5 * time.Second,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     time.Minute,
		MaxPlansPerTick:   1,
		RunTimeout:        45 * time.Second,
		StaleTimeout:      90 * time.Second,
		HeartbeatInterval: 15 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       5,
		RetryBackoff:      []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			if execution == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "issue_execution_unavailable"}}, nil
			}
			processed, err := execution.RecoverMissing(ctx, issueDispatchRecoveryBatch)
			if err != nil {
				return HandlerResult{}, err
			}
			return HandlerResult{RowsAffected: int64(processed), Result: map[string]any{"processed": processed}}, nil
		},
	}
}

const issueDispatchRecoveryBatch int32 = 100
