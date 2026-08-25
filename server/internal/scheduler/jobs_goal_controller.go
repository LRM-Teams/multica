package scheduler

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

const JobNameGoalController = "goal_controller_dispatch"

// GoalControllerJob drains standard Goal control-plane events into one
// manager reconciliation Run per Goal. Agent delivery/ack remains the Run
// lifecycle authority after dispatch.
func GoalControllerJob(h *handler.Handler) JobSpec {
	return JobSpec{
		Name:              JobNameGoalController,
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
			if h == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "handler_unavailable"}}, nil
			}
			processed, err := h.DispatchGoalControllerEvents(ctx, 32)
			if err != nil {
				return HandlerResult{}, err
			}
			return HandlerResult{RowsAffected: int64(processed), Result: map[string]any{"goals_dispatched": processed}}, nil
		},
	}
}
