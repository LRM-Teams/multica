package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/handler"
)

const JobNameWendyHandoffDispatch = "wendy_handoff_dispatch"

const (
	wendyHandoffDispatchCadence = 5 * time.Minute
	wendyHandoffDispatchLimit   = int32(10)
)

func WendyHandoffDispatchJob(h *handler.Handler, pool *pgxpool.Pool) JobSpec {
	return JobSpec{
		Name:              JobNameWendyHandoffDispatch,
		Cadence:           wendyHandoffDispatchCadence,
		ScheduleDelay:     wendyHandoffDispatchCadence,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     wendyHandoffDispatchCadence,
		MaxPlansPerTick:   1,
		RunTimeout:        10 * time.Second,
		StaleTimeout:      time.Minute,
		HeartbeatInterval: 5 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{15 * time.Second, time.Minute, 5 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			if pool == nil || h == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "unavailable"}}, nil
			}
			dispatched, err := h.DispatchDueWendyHandoffs(ctx, wendyHandoffDispatchLimit)
			if err != nil {
				return HandlerResult{}, err
			}
			ambient, ambientErr := h.DispatchDueWendyAmbientReviews(ctx, wendyHandoffDispatchLimit)
			if ambientErr != nil {
				return HandlerResult{
					RowsAffected: int64(dispatched),
					Result: map[string]any{
						"dispatched": dispatched,
						"ambient":    ambient,
					},
				}, ambientErr
			}
			// Idle nudge: never let a team go fully idle while its goal is
			// unfinished — trigger Beckham to look and get someone working.
			idle, idleErr := h.DispatchIdleNudges(ctx, wendyHandoffDispatchLimit)
			if idleErr != nil {
				return HandlerResult{
					RowsAffected: int64(dispatched + ambient),
					Result: map[string]any{
						"dispatched":  dispatched,
						"ambient":     ambient,
						"idle_nudged": idle,
					},
				}, idleErr
			}
			return HandlerResult{
				RowsAffected: int64(dispatched + ambient + idle),
				Result: map[string]any{
					"dispatched":  dispatched,
					"ambient":     ambient,
					"idle_nudged": idle,
				},
			}, nil
		},
	}
}
