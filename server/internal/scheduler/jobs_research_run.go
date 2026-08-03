package scheduler

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

const JobNameResearchRunReconcile = "research_run_reconcile"

func ResearchRunReconcileJob(h *handler.Handler) JobSpec {
	return JobSpec{
		Name:              JobNameResearchRunReconcile,
		Cadence:           15 * time.Second,
		ScheduleDelay:     0,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     time.Minute,
		MaxPlansPerTick:   1,
		RunTimeout:        2 * time.Minute,
		StaleTimeout:      3 * time.Minute,
		HeartbeatInterval: 20 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       5,
		RetryBackoff:      []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			if h == nil || h.ResearchRun == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "research_run_engine_unavailable"}}, nil
			}
			processed, err := h.ProcessDueResearchRuns(ctx, 100)
			if err != nil {
				return HandlerResult{}, err
			}
			return HandlerResult{RowsAffected: int64(processed), Result: map[string]any{"processed": processed}}, nil
		},
	}
}
