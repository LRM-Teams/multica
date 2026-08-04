package scheduler

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

const JobNameReminderOverdueScan = "reminder_overdue_scan"

// ReminderOverdueScanJob periodically finds scheduled reminders past fire_at
// and emits user-facing Activity events (task #67).
func ReminderOverdueScanJob(h *handler.Handler) JobSpec {
	const cadence = 15 * time.Minute
	return JobSpec{
		Name:              JobNameReminderOverdueScan,
		Cadence:           cadence,
		ScheduleDelay:     0,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     30 * time.Minute,
		MaxPlansPerTick:   1,
		RunTimeout:        2 * time.Minute,
		StaleTimeout:      5 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       2,
		RetryBackoff:      []time.Duration{time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			if h == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "handler_unavailable"}}, nil
			}
			n, err := h.ProcessOverdueReminders(ctx, 100)
			if err != nil {
				return HandlerResult{}, err
			}
			return HandlerResult{
				RowsAffected: int64(n),
				Result:       map[string]any{"overdue_events_emitted": n},
			}, nil
		},
	}
}
