package handler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// overdueReminderThreshold is how long past fire_at a scheduled reminder must
// remain unfired before we treat it as "not discovered" (task #67). Wider than
// the local retry window so transient delays do not spam Activity.
const overdueReminderThreshold = time.Hour

const reminderOverdueEventType = "reminder_overdue"

// ProcessOverdueReminders scans for scheduled reminders past fire_at by more
// than overdueReminderThreshold and emits one user-facing Activity event per
// reminder (idempotent via details.reminder_id). Server-side discovery so
// stuck timers surface without someone opening the reminders tab (Nash design).
func (h *Handler) ProcessOverdueReminders(ctx context.Context, limit int) (int, error) {
	if h == nil || h.DB == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := h.DB.Query(ctx, `
		SELECT r.id, r.workspace_id, r.agent_id, r.title, r.fire_at, a.runtime_id
		FROM agent_reminder r
		JOIN agent a ON a.id = r.agent_id AND a.archived_at IS NULL
		WHERE r.status = 'scheduled'
		  AND r.fire_at IS NOT NULL
		  AND r.fire_at < now() - make_interval(secs => $1::double precision)
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_activity_event e
		    WHERE e.agent_id = r.agent_id
		      AND e.event_type = $2
		      AND e.details->>'reminder_id' = r.id::text
		  )
		ORDER BY r.fire_at ASC
		LIMIT $3`,
		overdueReminderThreshold.Seconds(), reminderOverdueEventType, limit,
	)
	if err != nil {
		return 0, fmt.Errorf("scan overdue reminders: %w", err)
	}
	defer rows.Close()

	type hit struct {
		id, workspaceID, agentID, runtimeID pgtype.UUID
		title                               string
		fireAt                              time.Time
	}
	var hits []hit
	for rows.Next() {
		var row hit
		var title pgtype.Text
		var fireAt pgtype.Timestamptz
		if err := rows.Scan(&row.id, &row.workspaceID, &row.agentID, &title, &fireAt, &row.runtimeID); err != nil {
			return 0, fmt.Errorf("scan overdue reminder row: %w", err)
		}
		row.title = title.String
		if fireAt.Valid {
			row.fireAt = fireAt.Time
		}
		hits = append(hits, row)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	emitted := 0
	now := time.Now()
	for _, row := range hits {
		overdueFor := now.Sub(row.fireAt).Round(time.Minute)
		if overdueFor < 0 {
			overdueFor = 0
		}
		title := row.title
		if title == "" {
			title = "(untitled)"
		}
		msg := fmt.Sprintf(
			"Reminder is overdue and still scheduled: %q (due %s, overdue %s). Check agent/daemon health or reschedule.",
			title,
			row.fireAt.UTC().Format(time.RFC3339),
			overdueFor.String(),
		)
		h.recordAgentActivityEvent(ctx, h.DB,
			row.workspaceID, row.agentID, row.runtimeID, pgtype.UUID{},
			activityKindCustom, reminderOverdueEventType, "warning",
			"agent", row.agentID, "",
			"reminder_overdue", msg,
			map[string]any{
				"reminder_id":   uuidToString(row.id),
				"fire_at":       row.fireAt.UTC().Format(time.RFC3339),
				"overdue_for_s": int64(overdueFor.Seconds()),
				"title":         title,
			},
		)
		emitted++
	}
	if emitted > 0 {
		slog.Info("reminder overdue scan: emitted activity events", "count", emitted)
	}
	return emitted, nil
}
