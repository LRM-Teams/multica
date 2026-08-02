package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// backfillAgentInboxToolCallFromResult merges completed tool Input into the
// matching started tool_call Activity row. Cursor often emits args only on
// completed; runtime_tool_event already carries that Input on tool_result.
// Existing path/query/pattern/tool_target values are never overwritten.
func (h *Handler) backfillAgentInboxToolCallFromResult(
	ctx context.Context,
	exec activityEventExec,
	workspaceID, agentID pgtype.UUID,
	deliveryID string,
	msg TaskMessageRequest,
) {
	if exec == nil || h == nil {
		return
	}
	callID := strings.TrimSpace(msg.CallID)
	deliveryID = strings.TrimSpace(deliveryID)
	if callID == "" || deliveryID == "" || len(msg.Input) == 0 {
		return
	}
	rawTool := strings.TrimSpace(msg.Tool)
	canonicalTool, known := taskMessageCanonicalToolName(rawTool, msg.Input)
	if !known {
		return
	}

	var eventID pgtype.UUID
	var detailsRaw []byte
	err := exec.QueryRow(ctx, `
		SELECT id, details
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_kind = $3
		  AND details->>'delivery_id' = $4
		  AND details->>'call_id' = $5
		ORDER BY created_at DESC
		LIMIT 1
	`, workspaceID, agentID, activityKindToolCall, deliveryID, callID).Scan(&eventID, &detailsRaw)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("agent activity event: tool_call backfill lookup failed",
				"error", err,
				"call_id", callID,
				"delivery_id", deliveryID,
				"agent_id", uuidToString(agentID),
			)
		}
		return
	}

	details := map[string]any{}
	if len(detailsRaw) > 0 {
		if err := json.Unmarshal(detailsRaw, &details); err != nil {
			slog.Warn("agent activity event: tool_call backfill decode failed",
				"error", err,
				"event_id", uuidToString(eventID),
			)
			return
		}
	}
	before, err := json.Marshal(details)
	if err != nil {
		return
	}
	agentActivityApplyToolSourceFacts(details, rawTool, canonicalTool, msg.Input)
	agentActivityApplyToolInputSummary(details, canonicalTool, msg.Input, false)
	if details["tool"] == nil {
		details["tool"] = canonicalTool
	}
	after, err := json.Marshal(details)
	if err != nil || bytes.Equal(before, after) {
		return
	}
	if _, err := exec.Exec(ctx, `
		UPDATE agent_activity_event
		SET details = $2::jsonb
		WHERE id = $1
	`, eventID, string(after)); err != nil {
		slog.Warn("agent activity event: tool_call backfill update failed",
			"error", err,
			"event_id", uuidToString(eventID),
			"call_id", callID,
		)
		return
	}
	if h.Bus == nil {
		return
	}
	workspaceIDString := uuidToString(workspaceID)
	event := h.hydrateAgentActivityTimelineEvent(ctx, workspaceIDString, agentID, eventID)
	if event == nil {
		return
	}
	h.publishAgentActivityRealtimeEvent(ctx, workspaceIDString, uuidToString(agentID), uuidToString(eventID), event, event.TargetRef)
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
	if internalHousekeepingFailureReason(reasonCode) {
		visibility = "diagnostic_only"
	}
	return visibility
}

// internalHousekeepingFailureReason identifies terminal outcomes that are
// internal bookkeeping working as designed, not something the user needs to
// react to. Task #48: agent_reassigned_elsewhere is #1628's stale-daemon
// self-eviction working correctly (a daemon that lost ownership of an agent
// stops retrying and reports a clean terminus instead of retrying forever)
// — but shown raw in the user's activity feed it reads exactly like an
// error, which is what prompted the confusion this fix addresses.
//
// restarted_by_user (task #62) is the same shape one layer down: a plain
// restart force-kills the agent's resident process, which the interrupted
// turn's own goroutine then reports through the normal task-failure path —
// same as a real crash, because from that goroutine's side it looks
// identical to one. Without this, a user-initiated restart would show up in
// their own activity feed as an unexplained crash for the thing they just
// asked to happen.
func internalHousekeepingFailureReason(reasonCode string) bool {
	switch reasonCode {
	case "agent_reassigned_elsewhere", "restarted_by_user":
		return true
	default:
		return false
	}
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
		"reminder_fired",
		agentLifecycleSucceededActivityEventType:
		return true
	default:
		return false
	}
}
