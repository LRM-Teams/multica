package scheduler

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

const JobNameResearchProductionWindow = "research_production_window"

func ResearchProductionWindowJob(h *handler.Handler) JobSpec {
	return JobSpec{
		Name:              JobNameResearchProductionWindow,
		Cadence:           15 * time.Minute,
		ScheduleDelay:     0,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     time.Hour,
		MaxPlansPerTick:   1,
		RunTimeout:        2 * time.Minute,
		StaleTimeout:      3 * time.Minute,
		HeartbeatInterval: 20 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{time.Minute, 5 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			if h == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "handler_unavailable"}}, nil
			}
			processed, err := h.ProcessDueResearchProductionWindows(ctx, 50)
			if err != nil {
				return HandlerResult{}, err
			}
			return HandlerResult{RowsAffected: int64(processed), Result: map[string]any{"processed": processed}}, nil
		},
	}
}
