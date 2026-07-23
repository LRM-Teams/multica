package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	activityKindThinking           = "thinking"
	activityKindToolCall           = "tool_call"
	activityKindToolOutput         = "tool_output"
	activityKindTurnEnd            = "turn_end"
	activityKindSessionInit        = "session_init"
	activityKindCompactionStarted  = "compaction_started"
	activityKindCompactionFinished = "compaction_finished"
	activityKindWakeAttempt        = "wake_attempt"
	activityKindError              = "error"
	activityKindText               = "text"
	activityKindSystem             = "system"
	activityKindTransport          = "transport"
	activityKindTelemetry          = "telemetry"
	activityKindBlocked            = "blocked"
	activityKindCustom             = "custom"

	activityCompactingContextMessage      = "Compacting context"
	activityContextCompactionFinishedText = "Context compaction finished"
)

// activityEventExec is the minimal Exec interface shared by Handler.DB
// (dbExecutor) and pgx.Tx, so activity events can be written inside or
// outside a transaction.
type activityEventExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
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
// agent. eventKind must be a Raft ActivityKind name; Multica-specific subtypes
// belong in eventType, reasonCode, or details.
//
// The function is fail-soft: a database error is logged at warn level but does
// not abort the caller's transaction, because activity events are observability
// records and should never block the business operation they describe.
func insertAgentActivityEvent(
	ctx context.Context,
	exec activityEventExec,
	workspaceID, agentID, runtimeID, taskID pgtype.UUID,
	eventKind, eventType, severity string,
	targetKind string, targetID pgtype.UUID, targetSlug string,
	reasonCode, message string,
	details map[string]any,
) (pgtype.UUID, bool) {
	if exec == nil {
		return pgtype.UUID{}, false
	}
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		slog.Warn("agent activity event: marshal details failed", "error", err, "event_type", eventType)
		return pgtype.UUID{}, false
	}
	var id pgtype.UUID
	err = exec.QueryRow(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, runtime_id, task_id,
			event_kind, event_type, severity,
			target_kind, target_id, target_slug,
			reason_code, message, details,
			visibility
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14)
		RETURNING id
	`,
		workspaceID, agentID, runtimeID, taskID,
		eventKind, eventType, severity,
		targetKind, targetID, targetSlug,
		reasonCode, message, string(payload),
		activityVisibilityFor(eventKind, eventType, severity, reasonCode),
	).Scan(&id)
	if err != nil {
		slog.Warn("agent activity event: insert failed",
			"error", err,
			"event_type", eventType,
			"agent_id", uuidToString(agentID),
			"task_id", uuidToString(taskID),
		)
		return pgtype.UUID{}, false
	}
	return id, true
}

func (h *Handler) recordAgentActivityEvent(
	ctx context.Context,
	exec activityEventExec,
	workspaceID, agentID, runtimeID, taskID pgtype.UUID,
	eventKind, eventType, severity string,
	targetKind string, targetID pgtype.UUID, targetSlug string,
	reasonCode, message string,
	details map[string]any,
) {
	id, ok := insertAgentActivityEvent(ctx, exec,
		workspaceID, agentID, runtimeID, taskID,
		eventKind, eventType, severity,
		targetKind, targetID, targetSlug,
		reasonCode, message, details,
	)
	if !ok || h == nil || h.Bus == nil {
		return
	}
	workspaceIDString := uuidToString(workspaceID)
	event := h.hydrateAgentActivityTimelineEvent(ctx, workspaceIDString, agentID, id)
	targetRef := AgentActivityTargetRef{Kind: textOrDefault(pgtype.Text{String: targetKind, Valid: strings.TrimSpace(targetKind) != ""}, "none"), ID: uuidToPtr(targetID)}
	if strings.TrimSpace(targetSlug) != "" {
		targetRef.Slug = stringPtr(strings.TrimSpace(targetSlug))
	}
	h.publishAgentActivityRealtimeEvent(ctx, workspaceIDString, uuidToString(agentID), uuidToString(id), event, targetRef)
}

func activityVisibilityFor(eventKind, eventType, severity, reasonCode string) string {
	visibility := "user_facing"
	switch eventKind {
	case activityKindToolOutput,
		activityKindTelemetry,
		activityKindTransport,
		activityKindCustom:
		visibility = "diagnostic_only"
	}
	if eventKind == activityKindCustom && customActivityEventIsNarrative(eventType, reasonCode) {
		visibility = "user_facing"
	}
	if strings.Contains(reasonCode, "freshness") {
		visibility = "diagnostic_only"
	}
	return visibility
}

func customActivityEventIsNarrative(eventType, reasonCode string) bool {
	if reasonCode == "radar_untrusted_target" {
		return false
	}
	if eventType == "radar_action_executed" && reasonCode == "no_action" {
		return false
	}
	if strings.Contains(eventType, "subagent") {
		return true
	}
	switch eventType {
	case agentInboxStatusChangedEventType,
		"radar_action_executed",
		"radar_action_failed",
		"reminder_scheduled",
		"reminder_snoozed",
		"reminder_updated",
		"reminder_cancelled",
		"reminder_fired":
		return true
	default:
		return false
	}
}
