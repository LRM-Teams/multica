package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/handler"
)

const JobNameAgentReminderFire = "agent_reminder_fire"

func AgentReminderFireJob(h *handler.Handler, pool *pgxpool.Pool) JobSpec {
	return JobSpec{
		Name:              JobNameAgentReminderFire,
		Cadence:           time.Minute,
		ScheduleDelay:     15 * time.Second,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     time.Minute,
		MaxPlansPerTick:   1,
		RunTimeout:        30 * time.Second,
		StaleTimeout:      2 * time.Minute,
		HeartbeatInterval: 10 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{15 * time.Second, time.Minute, 5 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
			if pool == nil || h == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "unavailable"}}, nil
			}
			if err := h.RecoverStuckFiringReminders(ctx); err != nil {
				return HandlerResult{}, err
			}
			if err := h.FireDueReminders(ctx); err != nil {
				return HandlerResult{}, err
			}
			return HandlerResult{Result: map[string]any{"ok": true, "plan_time": in.PlanTime.UTC().Format(time.RFC3339)}}, nil
		},
	}
}
