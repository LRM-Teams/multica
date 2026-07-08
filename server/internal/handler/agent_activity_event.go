package handler

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// activityEventExec is the minimal Exec interface shared by Handler.DB
// (dbExecutor) and pgx.Tx, so activity events can be written inside or
// outside a transaction.
type activityEventExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// truncateForActivity trims a string to maxRunes for safe inclusion in
// activity event details, avoiding unbounded payload sizes.
func truncateForActivity(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

// recordAgentActivityEvent inserts a row into agent_activity_event for a single
// agent. It is the generic write point for all non-health lifecycle/platform
// events: run start/end, subagent lifecycle, wake trigger, suppression, etc.
//
// The function is fail-soft: a database error is logged at warn level but does
// not abort the caller's transaction, because activity events are observability
// records and should never block the business operation they describe.
func recordAgentActivityEvent(
	ctx context.Context,
	exec activityEventExec,
	workspaceID, agentID, runtimeID, taskID pgtype.UUID,
	eventKind, eventType, severity string,
	targetKind string, targetID pgtype.UUID, targetSlug string,
	reasonCode, message string,
	details map[string]any,
) {
	if exec == nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		slog.Warn("agent activity event: marshal details failed", "error", err, "event_type", eventType)
		return
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, runtime_id, task_id,
			event_kind, event_type, severity,
			target_kind, target_id, target_slug,
			reason_code, message, details
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb)
	`,
		workspaceID, agentID, runtimeID, taskID,
		eventKind, eventType, severity,
		targetKind, targetID, targetSlug,
		reasonCode, message, string(payload),
	)
	if err != nil {
		slog.Warn("agent activity event: insert failed",
			"error", err,
			"event_type", eventType,
			"agent_id", uuidToString(agentID),
			"task_id", uuidToString(taskID),
		)
	}
}
