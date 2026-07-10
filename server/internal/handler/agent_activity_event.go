package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
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
	narrative := agentActivityEventNarrative(eventKind, eventType, severity, reasonCode, message)
	var id pgtype.UUID
	err = exec.QueryRow(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, runtime_id, task_id,
			event_kind, event_type, severity,
			target_kind, target_id, target_slug,
			reason_code, message, details,
			visibility, action_label, summary, reason_label, tone
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14, $15, $16, $17, $18)
		RETURNING id
	`,
		workspaceID, agentID, runtimeID, taskID,
		eventKind, eventType, severity,
		targetKind, targetID, targetSlug,
		reasonCode, message, string(payload),
		narrative.Visibility, narrative.Label, narrative.Summary, narrative.ReasonLabel, narrative.Tone,
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
	h.publish(protocol.EventAgentActivityEvent, uuidToString(workspaceID), "system", "", AgentActivityEventRealtimePayload{
		AgentID: uuidToString(agentID),
		EventID: uuidToString(id),
	})
}

type activityNarrative struct {
	Visibility  string
	Label       string
	Summary     string
	ReasonLabel string
	Tone        string
}

func agentActivityEventNarrative(eventKind, eventType, severity, reasonCode, message string) activityNarrative {
	visibility := "user_facing"
	tone := "action"
	label := humanizeActivityToken(eventType)
	summary := strings.TrimSpace(message)
	if summary == "" {
		summary = label + "."
	}
	reasonLabel := humanizeActivityToken(reasonCode)
	if strings.TrimSpace(reasonCode) == "" {
		reasonLabel = ""
	}

	switch eventKind {
	case activityKindWakeAttempt:
		label, tone = "Wake attempt", "wake"
		if summary == "" || summary == label+"." {
			summary = "Agent was woken for new work."
		}
	case activityKindTurnEnd:
		label, summary, tone = "Run completed", "Finished the run.", "success"
	case activityKindError:
		label, tone = "Run failed", "failure"
	case activityKindBlocked:
		label, tone = "Blocked", "muted"
	case activityKindText:
		label, tone = "Sent a message", "success"
	case activityKindTransport:
		switch eventType {
		case "server_ping_received":
			label, summary, tone = "Runtime online", "Runtime heartbeat received.", "success"
		case "daemon_liveness_probe_sent":
			label, summary, tone = "Checking runtime", "Checking runtime liveness.", "progress"
		case "probe_timeout_reconnect":
			label, summary, tone = "Runtime reconnecting", "Runtime missed a liveness check.", "failure"
		case "transport_reconnected":
			label, summary, tone = "Runtime recovered", "Runtime transport reconnected.", "success"
		default:
			label, tone = "Runtime transport", "progress"
		}
	case activityKindCustom:
		visibility = "diagnostic_only"
		tone = "muted"
	}

	if severity == "error" {
		tone = "failure"
	}
	if strings.Contains(reasonCode, "freshness") {
		visibility = "diagnostic_only"
		tone = "muted"
	}
	if summary == "" {
		summary = label + "."
	}
	return activityNarrative{Visibility: visibility, Label: label, Summary: summary, ReasonLabel: reasonLabel, Tone: tone}
}

func humanizeActivityToken(token string) string {
	token = strings.TrimSpace(strings.ReplaceAll(token, "_", " "))
	if token == "" {
		return "Activity recorded"
	}
	return strings.ToUpper(token[:1]) + token[1:]
}
