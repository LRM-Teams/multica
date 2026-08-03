package scheduler

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

const JobNameResearchNextStep = "research_nextstep_scheduler"

// ResearchNextStepJob scans running unattended research sessions and emits
// next-step work items + wakes (LRM-1076).
func ResearchNextStepJob(h *handler.Handler) JobSpec {
	const cadence = 1 * time.Minute
	return JobSpec{
		Name:              JobNameResearchNextStep,
		Cadence:           cadence,
		ScheduleDelay:     0,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     5 * time.Minute,
		MaxPlansPerTick:   1,
		RunTimeout:        2 * time.Minute,
		StaleTimeout:      3 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{30 * time.Second, time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			if h == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "handler_unavailable"}}, nil
			}
			processed, err := h.ProcessResearchNextSteps(ctx, 32)
			if err != nil {
				return HandlerResult{}, err
			}
			return HandlerResult{
				RowsAffected: int64(processed),
				Result:       map[string]any{"work_items_enqueued": processed},
			}, nil
		},
	}
}
