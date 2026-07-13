package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

const (
	agentInboxDrainMessageLimit      = 50
	agentInboxStatusChangedEventType = "agent_status_changed"
	agentInboxStatusActivityWorking  = "working"
	agentInboxStatusActivityIdle     = "idle"
)

var claimedFileDeliveryRe = regexp.MustCompile(`(?i)\b[\w.-]+\.(txt|md|pdf|csv|xlsx|xls|docx|png|jpe?g|gif|zip|json|ya?ml|go|ts|tsx|js|py|html|css)\b`)

type DrainAgentInboxResponse struct {
	Events      []AgentInboxEventResponse `json:"events"`
	LastSeenSeq int64                     `json:"last_seen_seq"`
	HasMore     bool                      `json:"has_more"`
}

type AgentInboxEventResponse struct {
	ID              string                   `json:"id"`
	DeliveryID      string                   `json:"delivery_id"`
	AgentSessionID  string                   `json:"agent_session_id"`
	ConversationID  string                   `json:"conversation_id"`
	ChannelID       string                   `json:"channel_id,omitempty"`
	ChatSessionID   string                   `json:"chat_session_id,omitempty"`
	AgentID         string                   `json:"agent_id"`
	SourceMessageID string                   `json:"source_message_id,omitempty"`
	Reason          string                   `json:"reason"`
	RequiresWake    bool                     `json:"requires_wake"`
	Priority        int32                    `json:"priority"`
	SeqFrom         int64                    `json:"seq_from"`
	SeqTo           int64                    `json:"seq_to"`
	Messages        []ChannelMessageResponse `json:"messages,omitempty"`
	LeaseToken      string                   `json:"lease_token"`
	LeaseExpiresAt  string                   `json:"lease_expires_at"`
	Task            *AgentTaskResponse       `json:"task,omitempty"`
}

type AgentInboxLeaseResponse struct {
	ID             string `json:"id"`
	DeliveryID     string `json:"delivery_id"`
	LeaseToken     string `json:"lease_token"`
	LeaseExpiresAt string `json:"lease_expires_at"`
	SeqTo          int64  `json:"seq_to"`
	RequiresWake   bool   `json:"requires_wake"`
}

func (h *Handler) publishAgentInboxTaskLifecycle(eventType string, event db.AgentInboxEvent, runtimeID pgtype.UUID, status string) {
	if h == nil || h.Bus == nil || !event.RequiresWake {
		return
	}
	payload := map[string]any{
		"task_id":        uuidToString(event.ID),
		"inbox_event_id": uuidToString(event.ID),
		"agent_id":       uuidToString(event.AgentID),
		"issue_id":       "",
		"status":         status,
	}
	if runtimeID.Valid {
		payload["runtime_id"] = uuidToString(runtimeID)
	}
	if event.ChatSessionID.Valid {
		payload["chat_session_id"] = uuidToString(event.ChatSessionID)
	}
	h.publishTask(eventType, uuidToString(event.WorkspaceID), "system", "", uuidToString(event.ID), payload)
}

type AckAgentInboxEventRequest struct {
	DeliveryID  string `json:"delivery_id"`
	LeaseToken  string `json:"lease_token"`
	SeenUpToSeq int64  `json:"seen_up_to_seq"`
}

type RenewAgentInboxEventRequest struct {
	DeliveryID string `json:"delivery_id"`
	LeaseToken string `json:"lease_token"`
}

type ReportAgentInboxMessagesRequest struct {
	DeliveryID string               `json:"delivery_id"`
	LeaseToken string               `json:"lease_token"`
	Messages   []TaskMessageRequest `json:"messages"`
}

type FailAgentInboxEventRequest struct {
	DeliveryID    string `json:"delivery_id"`
	LeaseToken    string `json:"lease_token"`
	Error         string `json:"error"`
	SessionID     string `json:"session_id,omitempty"`
	WorkDir       string `json:"work_dir,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	ReasonCode    string `json:"reason_code,omitempty"`
}

type CompleteAgentInboxEventRequest struct {
	DeliveryID string `json:"delivery_id"`
	LeaseToken string `json:"lease_token"`
	TaskCompleteRequest
}

func (h *Handler) DrainAgentInboxByRuntime(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	if err := h.Queries.ReclaimExpiredAgentInboxDeliveriesForRuntime(r.Context(), runtime.ID); err != nil {
		slog.Warn("agent inbox drain: failed to reclaim expired deliveries", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to reclaim expired inbox deliveries")
		return
	}
	delivery, err := h.Queries.LeaseAgentInboxEventForRuntime(r.Context(), runtime.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, DrainAgentInboxResponse{Events: []AgentInboxEventResponse{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to drain agent inbox")
		return
	}
	event, err := h.Queries.GetAgentInboxEvent(r.Context(), delivery.InboxEventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load inbox event")
		return
	}
	if event.WorkspaceID != runtime.WorkspaceID {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	pending, _ := h.Queries.CountPendingAgentInboxEventsForRuntime(r.Context(), runtime.ID)
	respEvent := h.agentInboxEventResponse(r.Context(), runtime, event, delivery)
	h.publishAgentInboxTaskLifecycle(protocol.EventTaskDispatch, event, runtime.ID, "running")
	writeJSON(w, http.StatusOK, DrainAgentInboxResponse{
		Events:      []AgentInboxEventResponse{respEvent},
		LastSeenSeq: event.SeqTo,
		HasMore:     pending > 0,
	})
}

func (h *Handler) AckAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "event_id")
	if !ok {
		return
	}
	event, ok := h.requireDaemonInboxEventAccess(w, r, eventID)
	if !ok {
		return
	}
	var req AckAgentInboxEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, req.DeliveryID, "delivery_id")
	if !ok {
		return
	}
	leaseToken, ok := parseUUIDOrBadRequest(w, req.LeaseToken, "lease_token")
	if !ok {
		return
	}
	seenUpToSeq := req.SeenUpToSeq
	if seenUpToSeq <= 0 || seenUpToSeq > event.SeqTo {
		seenUpToSeq = event.SeqTo
	}
	if seenUpToSeq < event.SeqTo {
		writeError(w, http.StatusConflict, "cannot ack before seeing the full event range")
		return
	}
	acked, err := h.Queries.AckAgentInboxDelivery(r.Context(), db.AckAgentInboxDeliveryParams{
		ID:           deliveryID,
		InboxEventID: event.ID,
		LeaseToken:   leaseToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to ack inbox delivery")
		return
	}
	h.recordAgentInboxStatusActivity(r.Context(), event, h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID), deliveryID, agentInboxStatusActivityIdle)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "acked_seq": acked.SeqTo})
}

func (h *Handler) RenewAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "event_id")
	if !ok {
		return
	}
	event, ok := h.requireDaemonInboxEventAccess(w, r, eventID)
	if !ok {
		return
	}
	var req RenewAgentInboxEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, req.DeliveryID, "delivery_id")
	if !ok {
		return
	}
	leaseToken, ok := parseUUIDOrBadRequest(w, req.LeaseToken, "lease_token")
	if !ok {
		return
	}
	delivery, err := h.Queries.RenewAgentInboxDelivery(r.Context(), db.RenewAgentInboxDeliveryParams{
		ID:           deliveryID,
		InboxEventID: event.ID,
		LeaseToken:   leaseToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to renew inbox delivery")
		return
	}
	writeJSON(w, http.StatusOK, AgentInboxLeaseResponse{
		ID:             uuidToString(event.ID),
		DeliveryID:     uuidToString(delivery.ID),
		LeaseToken:     uuidToString(delivery.LeaseToken),
		LeaseExpiresAt: timestampToString(delivery.LeaseExpiresAt),
		SeqTo:          event.SeqTo,
		RequiresWake:   event.RequiresWake,
	})
}

func (h *Handler) ReportAgentInboxMessages(w http.ResponseWriter, r *http.Request) {
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "event_id")
	if !ok {
		return
	}
	event, ok := h.requireDaemonInboxEventAccess(w, r, eventID)
	if !ok {
		return
	}
	var req ReportAgentInboxMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Messages) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, req.DeliveryID, "delivery_id")
	if !ok {
		return
	}
	leaseToken, ok := parseUUIDOrBadRequest(w, req.LeaseToken, "lease_token")
	if !ok {
		return
	}
	runtimeID, ok := h.requireActiveAgentInboxDelivery(w, r, event, deliveryID, leaseToken)
	if !ok {
		return
	}
	targetKind, targetID := agentInboxActivityTarget(event)
	for _, msg := range req.Messages {
		msg.Content = redact.Text(msg.Content)
		msg.Output = redact.Text(msg.Output)
		msg.Input = redact.InputMap(msg.Input)
		kind, eventType, severity := agentInboxActivityMessageKind(msg.Type)
		message := agentInboxActivityMessageText(msg)
		details := map[string]any{
			"inbox_event_id":    uuidToString(event.ID),
			"delivery_id":       uuidToString(deliveryID),
			"source_message_id": uuidToString(event.SourceMessageID),
			"seq":               msg.Seq,
		}
		if msg.Type == "tool_use" {
			rawTool := strings.TrimSpace(msg.Tool)
			canonicalTool, known := taskMessageCanonicalToolName(rawTool, msg.Input)
			if command := redactedCommandFromInput(msg.Input); command != "" {
				details["command"] = command
			}
			if cli, ok := resolveRaftCLIInvocation(canonicalTool, msg.Input); ok {
				canonicalTool = cli.Tool
				known = true
				for key, value := range cli.Details {
					details[key] = value
				}
			}
			if !known {
				if rawTool != "" {
					details["unmapped_tool_name"] = rawTool
				}
				if target, summaryKind := agentInboxActivityToolTarget(msg); target != "" {
					details["tool_target"] = target
					details["summary_kind"] = summaryKind
				}
				h.recordAgentActivityEvent(r.Context(), h.DB,
					event.WorkspaceID, event.AgentID, runtimeID, pgtype.UUID{},
					activityKindCustom, "unmapped_tool_name", "warning",
					targetKind, targetID, "",
					"unmapped_tool_name", "Unmapped runtime tool name",
					details,
				)
				continue
			}
			details["tool"] = canonicalTool
			if canonicalTool != rawTool {
				details["raw_tool"] = rawTool
			}
			agentActivityApplyToolSourceFacts(details, rawTool, canonicalTool, msg.Input)
			agentActivityApplyToolInputSummary(details, canonicalTool, msg.Input, false)
		}
		if msg.Type == "tool_use" {
			if target, summaryKind := agentInboxActivityToolTarget(msg); target != "" && details["tool_target"] == nil {
				details["tool_target"] = target
				details["summary_kind"] = summaryKind
			}
		}
		h.recordAgentActivityEvent(r.Context(), h.DB,
			event.WorkspaceID, event.AgentID, runtimeID, pgtype.UUID{},
			kind, eventType, severity,
			targetKind, targetID, "",
			"", message,
			details,
		)
		if payload, ok := agentInboxTaskMessagePayload(event, msg, kind, details); ok && h.Bus != nil {
			h.publishTask(protocol.EventTaskMessage, uuidToString(event.WorkspaceID), "system", "", uuidToString(event.ID), payload)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) CompleteAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "event_id")
	if !ok {
		return
	}
	event, ok := h.requireDaemonInboxEventAccess(w, r, eventID)
	if !ok {
		return
	}
	var req CompleteAgentInboxEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, req.DeliveryID, "delivery_id")
	if !ok {
		return
	}
	leaseToken, ok := parseUUIDOrBadRequest(w, req.LeaseToken, "lease_token")
	if !ok {
		return
	}

	task := agentInboxSyntheticTask(event, h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID))
	if err := h.normalizeTaskCompleteOutput(r.Context(), task, &req.TaskCompleteRequest); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusInternalServerError, "transaction starter unavailable")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin inbox complete transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	acked, err := qtx.AckAgentInboxDelivery(r.Context(), db.AckAgentInboxDeliveryParams{
		ID:           deliveryID,
		InboxEventID: event.ID,
		LeaseToken:   leaseToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to complete inbox delivery")
		return
	}
	var chatDonePayload *protocol.ChatDonePayload
	if event.ChatSessionID.Valid {
		var sessionRuntimeID pgtype.UUID
		if req.SessionID != "" {
			sessionRuntimeID = task.RuntimeID
		}
		if err := qtx.UpdateChatSessionSession(r.Context(), db.UpdateChatSessionSessionParams{
			ID:        event.ChatSessionID,
			SessionID: pgtype.Text{String: req.SessionID, Valid: req.SessionID != ""},
			WorkDir:   pgtype.Text{String: req.WorkDir, Valid: req.WorkDir != ""},
			RuntimeID: sessionRuntimeID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update chat session")
			return
		}
		payload, err := h.completedAgentInboxChatPayload(r.Context(), qtx, event, req.TaskCompleteRequest)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save inbox chat output")
			return
		}
		chatDonePayload = payload
	}
	if _, err := qtx.SetAgentInboxTerminalOutcome(r.Context(), db.SetAgentInboxTerminalOutcomeParams{
		ID:                 event.ID,
		WorkspaceID:        event.WorkspaceID,
		TerminalOutcome:    strToText(agentInboxCompletionTerminalOutcome(r.Context(), h, event, req.TaskCompleteRequest, chatDonePayload)),
		TerminalDeliveryID: deliveryID,
		Retryable:          false,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record inbox terminal outcome")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit inbox completion")
		return
	}
	h.TaskService.RecordEvolutionSkillOutcome(r.Context(), event.ID, "success", "success")
	if chatDonePayload != nil {
		h.publishAgentInboxChatDone(event, *chatDonePayload)
		h.recordAgentInboxVisibleOutputActivity(r.Context(), event, task.RuntimeID, *chatDonePayload)
	}
	h.recordAgentInboxStatusActivity(r.Context(), event, task.RuntimeID, deliveryID, agentInboxStatusActivityIdle)
	h.publishAgentInboxTaskLifecycle(protocol.EventTaskCompleted, event, task.RuntimeID, "completed")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "acked_seq": acked.SeqTo})
}

func agentInboxCompletionTerminalOutcome(ctx context.Context, h *Handler, event db.AgentInboxEvent, req TaskCompleteRequest, payload *protocol.ChatDonePayload) string {
	if h.inboxEventHasAgentTransportVisibleOutput(ctx, event.ID) {
		return "replied"
	}
	if payload != nil {
		switch payload.Type {
		case protocol.ChatOutputKindNoReply:
			return "no_reply"
		case protocol.ChatOutputKindMessage, protocol.ChatOutputKindReaction:
			return "replied"
		}
	}
	if req.Action == protocol.ChatOutputActionNoReply || req.Type == protocol.ChatOutputKindNoReply {
		return "no_reply"
	}
	return "replied"
}

func (h *Handler) FailAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "event_id")
	if !ok {
		return
	}
	event, ok := h.requireDaemonInboxEventAccess(w, r, eventID)
	if !ok {
		return
	}
	var req FailAgentInboxEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, req.DeliveryID, "delivery_id")
	if !ok {
		return
	}
	leaseToken, ok := parseUUIDOrBadRequest(w, req.LeaseToken, "lease_token")
	if !ok {
		return
	}
	errText := strings.TrimSpace(req.Error)
	if errText == "" {
		errText = "agent inbox delivery failed"
	}
	failureReason := strings.TrimSpace(req.FailureReason)
	reasonCode := agentInboxFailureReasonCode(errText, failureReason, req.ReasonCode)
	if failureReason != "" {
		h.completeFailedAgentInboxEvent(w, r, event, deliveryID, leaseToken, errText, failureReason, reasonCode, req.SessionID, req.WorkDir)
		return
	}
	failed, err := h.Queries.FailAgentInboxDelivery(r.Context(), db.FailAgentInboxDeliveryParams{
		ID:           deliveryID,
		InboxEventID: event.ID,
		LastError:    strToText(errText),
		LeaseToken:   leaseToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to mark inbox delivery failed")
		return
	}

	failedEvent := failedAgentInboxEvent(failed)
	h.recordAgentInboxFailureActivity(r.Context(), failedEvent, deliveryID, errText, failureReason, reasonCode)
	h.publishAgentInboxTaskLifecycle(protocol.EventTaskQueued, failedEvent, h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID), "queued")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func agentInboxFailureReasonCode(errText, failureReason, reasonCode string) string {
	reasonCode = strings.TrimSpace(reasonCode)
	if reasonCode == "" {
		reasonCode = strings.TrimSpace(failureReason)
	}
	if strings.Contains(errText, agent.ProviderAuthRequiredMarker) {
		reasonCode = agent.ProviderAuthRequiredMarker
	}
	return reasonCode
}

func failedAgentInboxEvent(row db.FailAgentInboxDeliveryRow) db.AgentInboxEvent {
	return db.AgentInboxEvent{
		ID:                 row.ID,
		WorkspaceID:        row.WorkspaceID,
		AgentSessionID:     row.AgentSessionID,
		ConversationID:     row.ConversationID,
		ChannelID:          row.ChannelID,
		ChatSessionID:      row.ChatSessionID,
		AgentID:            row.AgentID,
		SourceMessageID:    row.SourceMessageID,
		Reason:             row.Reason,
		RequiresWake:       row.RequiresWake,
		Status:             row.Status,
		Priority:           row.Priority,
		SeqFrom:            row.SeqFrom,
		SeqTo:              row.SeqTo,
		Attempt:            row.Attempt,
		LastError:          row.LastError,
		ClaimedAt:          row.ClaimedAt,
		AckedAt:            row.AckedAt,
		TerminalOutcome:    row.TerminalOutcome,
		TerminalDeliveryID: row.TerminalDeliveryID,
		Retryable:          row.Retryable,
		TerminalAt:         row.TerminalAt,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

func (h *Handler) recordAgentInboxFailureActivity(ctx context.Context, event db.AgentInboxEvent, deliveryID pgtype.UUID, errText, failureReason, reasonCode string) {
	delivery, err := h.Queries.GetAgentEventDelivery(ctx, deliveryID)
	if err != nil {
		slog.Warn("agent inbox fail: failed to reload delivery for activity event", "delivery_id", uuidToString(deliveryID), "error", err)
	}
	targetKind := "agent"
	targetID := event.AgentID
	if event.ChannelID.Valid {
		targetKind = "channel"
		targetID = event.ChannelID
	} else if event.ChatSessionID.Valid {
		targetKind = "dm"
		targetID = event.ChatSessionID
	}
	h.TaskService.RecordEvolutionSkillOutcome(ctx, event.ID, "failure", "failure")
	h.recordAgentActivityEvent(ctx, h.DB,
		event.WorkspaceID, event.AgentID, delivery.RuntimeID, pgtype.UUID{},
		activityKindError, "agent_inbox_failed", "error",
		targetKind, targetID, "",
		reasonCode, "Agent inbox delivery failed: "+truncateForActivity(errText, 200),
		map[string]any{
			"failure_reason":    failureReason,
			"reason_code":       reasonCode,
			"inbox_event_id":    uuidToString(event.ID),
			"delivery_id":       uuidToString(deliveryID),
			"source_message_id": uuidToString(event.SourceMessageID),
		},
	)
}

func (h *Handler) completeFailedAgentInboxEvent(w http.ResponseWriter, r *http.Request, event db.AgentInboxEvent, deliveryID, leaseToken pgtype.UUID, errText, failureReason, reasonCode, sessionID, workDir string) {
	if h.TxStarter == nil {
		writeError(w, http.StatusInternalServerError, "transaction starter unavailable")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin inbox failure transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	acked, err := qtx.AckAgentInboxDelivery(r.Context(), db.AckAgentInboxDeliveryParams{
		ID:           deliveryID,
		InboxEventID: event.ID,
		LeaseToken:   leaseToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to ack failed inbox delivery")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE agent_inbox_event
		SET last_error = $2,
		    updated_at = now()
		WHERE id = $1`, event.ID, errText); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record inbox failure")
		return
	}
	if _, err := qtx.SetAgentInboxTerminalOutcome(r.Context(), db.SetAgentInboxTerminalOutcomeParams{
		ID:                 event.ID,
		WorkspaceID:        event.WorkspaceID,
		TerminalOutcome:    strToText("failed"),
		TerminalDeliveryID: deliveryID,
		Retryable:          true,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record inbox failure outcome")
		return
	}

	if event.ChatSessionID.Valid {
		if chatFailureResumeUnsafe(failureReason) {
			if _, err := tx.Exec(r.Context(), `
				UPDATE chat_session
				SET session_id = NULL,
				    work_dir = NULL,
				    runtime_id = NULL,
				    updated_at = now()
				WHERE id = $1`, event.ChatSessionID); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to clear unsafe chat session")
				return
			}
		} else if sessionID != "" || workDir != "" {
			var sessionRuntimeID pgtype.UUID
			if sessionID != "" {
				sessionRuntimeID = h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID)
			}
			if err := qtx.UpdateChatSessionSession(r.Context(), db.UpdateChatSessionSessionParams{
				ID:        event.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
				WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
				RuntimeID: sessionRuntimeID,
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update chat session")
				return
			}
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit inbox failure")
		return
	}
	runtimeID := h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID)
	h.publishAgentInboxTaskLifecycle(protocol.EventTaskFailed, event, runtimeID, "failed")
	h.recordAgentInboxFailureActivity(r.Context(), event, deliveryID, errText, failureReason, reasonCode)
	h.recordAgentInboxStatusActivity(r.Context(), event, runtimeID, deliveryID, agentInboxStatusActivityIdle)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "acked_seq": acked.SeqTo})
}

func (h *Handler) RetryChannelAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "inbox event id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}

	retry, err := h.Queries.RetryAgentInboxEvent(r.Context(), db.RetryAgentInboxEventParams{
		ID:          eventID,
		WorkspaceID: parseUUID(workspaceID),
		ChannelID:   channelID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "inbox event is not retryable")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to retry inbox event")
		return
	}
	if retryEvent, err := h.Queries.GetAgentInboxEvent(r.Context(), retry.ID); err == nil {
		h.publishAgentInboxTaskLifecycle(protocol.EventTaskQueued, retryEvent, h.runtimeIDForAgentInboxEvent(r.Context(), retryEvent), "queued")
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":             true,
		"inbox_event_id": uuidToString(retry.ID),
		"agent_id":       uuidToString(retry.AgentID),
		"status":         retry.Status,
	})
}

func (h *Handler) agentInboxEventResponse(ctx context.Context, runtime db.AgentRuntime, event db.AgentInboxEvent, delivery db.AgentEventDelivery) AgentInboxEventResponse {
	resp := AgentInboxEventResponse{
		ID:              uuidToString(event.ID),
		DeliveryID:      uuidToString(delivery.ID),
		AgentSessionID:  uuidToString(event.AgentSessionID),
		ConversationID:  uuidToString(event.ConversationID),
		ChannelID:       uuidToString(event.ChannelID),
		ChatSessionID:   uuidToString(event.ChatSessionID),
		AgentID:         uuidToString(event.AgentID),
		SourceMessageID: uuidToString(event.SourceMessageID),
		Reason:          event.Reason,
		RequiresWake:    event.RequiresWake,
		Priority:        event.Priority,
		SeqFrom:         event.SeqFrom,
		SeqTo:           event.SeqTo,
		LeaseToken:      uuidToString(delivery.LeaseToken),
		LeaseExpiresAt:  timestampToString(delivery.LeaseExpiresAt),
	}
	if event.ChannelID.Valid && event.SeqTo > 0 {
		from := event.SeqFrom - 1
		if from < 0 {
			from = 0
		}
		resp.Messages = h.channelAmbientUnreadMessages(ctx, h.DB, uuidToString(event.WorkspaceID), uuidToString(event.ChannelID), from, event.SeqTo, agentInboxDrainMessageLimit)
	}
	if event.RequiresWake && event.ChatSessionID.Valid {
		if task := h.agentInboxTaskResponse(ctx, runtime, event, delivery); task != nil {
			resp.Task = task
		}
	}
	return resp
}

func (h *Handler) agentInboxTaskResponse(ctx context.Context, runtime db.AgentRuntime, event db.AgentInboxEvent, delivery db.AgentEventDelivery) *AgentTaskResponse {
	task := agentInboxSyntheticTask(event, runtime.ID)
	runtimeWorkspaceID := uuidToString(runtime.WorkspaceID)
	resp := taskToResponse(task, runtimeWorkspaceID)
	resp.InboxEvent = &AgentInboxLeaseResponse{
		ID:             uuidToString(event.ID),
		DeliveryID:     uuidToString(delivery.ID),
		LeaseToken:     uuidToString(delivery.LeaseToken),
		LeaseExpiresAt: timestampToString(delivery.LeaseExpiresAt),
		SeqTo:          event.SeqTo,
		RequiresWake:   event.RequiresWake,
	}
	if !h.populateAgentInboxChatContext(ctx, event, &resp) {
		slog.Warn("agent inbox claim: exact prompt missing",
			"inbox_event_id", uuidToString(event.ID),
			"chat_session_id", uuidToString(event.ChatSessionID),
		)
		return nil
	}
	if agent, err := h.Queries.GetAgent(ctx, event.AgentID); err == nil {
		skills := h.TaskService.LoadAgentSkillsForInbox(ctx, event.AgentID, event.ID)
		skills = append(skills, h.TaskService.BuiltinSkills()...)
		var customEnv map[string]string
		if agent.CustomEnv != nil {
			if err := json.Unmarshal(agent.CustomEnv, &customEnv); err != nil {
				slog.Warn("agent inbox claim: failed to unmarshal agent custom_env", "agent_id", uuidToString(agent.ID), "error", err)
			}
		}
		var customArgs []string
		if agent.CustomArgs != nil {
			if err := json.Unmarshal(agent.CustomArgs, &customArgs); err != nil {
				slog.Warn("agent inbox claim: failed to unmarshal agent custom_args", "agent_id", uuidToString(agent.ID), "error", err)
			}
		}
		var mcpConfig json.RawMessage
		if agent.McpConfig != nil {
			mcpConfig = json.RawMessage(agent.McpConfig)
		}
		resp.Agent = &TaskAgentData{
			ID:            uuidToString(agent.ID),
			Name:          agentDisplayName(agent),
			Instructions:  agent.Instructions,
			Skills:        skills,
			CustomEnv:     customEnv,
			CustomArgs:    customArgs,
			McpConfig:     mcpConfig,
			Model:         agent.Model.String,
			ThinkingLevel: agent.ThinkingLevel.String,
		}
	}
	usesAgentCredentialTransport := runtime.OwnerID.Valid && agentRuntimeHasCapability(runtime, protocol.DaemonCapabilityAgentCredentialTransport)
	if runtime.OwnerID.Valid {
		if owner, err := h.Queries.GetUser(ctx, runtime.OwnerID); err == nil {
			resp.RequestingUserName = userDisplayName(owner)
			resp.RequestingUserProfileDescription = owner.ProfileDescription
		}
		if !usesAgentCredentialTransport {
			tokenStr, err := auth.GenerateAgentInboxDeliveryToken()
			if err != nil {
				slog.Error("agent inbox claim: failed to generate inbox token",
					"inbox_event_id", uuidToString(event.ID),
					"error", err,
				)
				return nil
			}
			if _, err := h.Queries.CreateAgentInboxToken(ctx, db.CreateAgentInboxTokenParams{
				TokenHash:    auth.HashToken(tokenStr),
				InboxEventID: event.ID,
				DeliveryID:   delivery.ID,
				AgentID:      event.AgentID,
				WorkspaceID:  event.WorkspaceID,
				UserID:       runtime.OwnerID,
				ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
			}); err != nil {
				slog.Error("agent inbox claim: failed to persist inbox token",
					"inbox_event_id", uuidToString(event.ID),
					"delivery_id", uuidToString(delivery.ID),
					"error", err,
				)
				return nil
			}
			resp.AuthToken = tokenStr
		}
	}
	if ws, err := h.Queries.GetWorkspace(ctx, event.WorkspaceID); err == nil && ws.Context.Valid {
		resp.WorkspaceContext = ws.Context.String
	}
	if resp.WorkspaceID == "" || resp.WorkspaceID != runtimeWorkspaceID {
		slog.Error("agent inbox claim: workspace isolation check failed",
			"inbox_event_id", uuidToString(event.ID),
			"runtime_id", uuidToString(runtime.ID),
			"runtime_workspace", runtimeWorkspaceID,
			"resolved_workspace", resp.WorkspaceID,
		)
		return nil
	}
	return &resp
}

func agentInboxSyntheticTask(event db.AgentInboxEvent, runtimeID pgtype.UUID) db.AgentTaskQueue {
	return db.AgentTaskQueue{
		ID:            event.ID,
		AgentID:       event.AgentID,
		RuntimeID:     runtimeID,
		ChatSessionID: event.ChatSessionID,
		Status:        "dispatched",
		Priority:      event.Priority,
		Attempt:       event.Attempt,
		MaxAttempts:   1,
		CreatedAt:     event.CreatedAt,
	}
}

func (h *Handler) requireActiveAgentInboxDelivery(w http.ResponseWriter, r *http.Request, event db.AgentInboxEvent, deliveryID, leaseToken pgtype.UUID) (pgtype.UUID, bool) {
	var runtimeID pgtype.UUID
	err := h.DB.QueryRow(r.Context(), `
		SELECT d.runtime_id
		FROM agent_event_delivery d
		WHERE d.id = $1
		  AND d.inbox_event_id = $2
		  AND d.lease_token = $3
		  AND d.status IN ('leased', 'processing')
		  AND d.lease_expires_at > now()
		  AND EXISTS (
		    SELECT 1
		    FROM agent_inbox_event e
		    WHERE e.id = d.inbox_event_id
		      AND e.agent_session_id = d.agent_session_id
		      AND e.status = 'draining'
		  )`, deliveryID, event.ID, leaseToken).Scan(&runtimeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active")
			return pgtype.UUID{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to verify inbox delivery")
		return pgtype.UUID{}, false
	}
	return runtimeID, true
}

func (h *Handler) runtimeIDForAgentInboxDelivery(ctx context.Context, deliveryID pgtype.UUID) pgtype.UUID {
	var runtimeID pgtype.UUID
	_ = h.DB.QueryRow(ctx, `
		SELECT runtime_id
		FROM agent_event_delivery
		WHERE id = $1`, deliveryID).Scan(&runtimeID)
	return runtimeID
}

func (h *Handler) runtimeIDForAgentInboxEvent(ctx context.Context, event db.AgentInboxEvent) pgtype.UUID {
	var runtimeID pgtype.UUID
	_ = h.DB.QueryRow(ctx, `
		SELECT COALESCE(latest_delivery.runtime_id, s.runtime_id, a.runtime_id)
		FROM agent_inbox_event e
		JOIN agent a ON a.id = e.agent_id
		LEFT JOIN agent_session s ON s.id = e.agent_session_id
		LEFT JOIN LATERAL (
			SELECT d.runtime_id
			FROM agent_event_delivery d
			WHERE d.inbox_event_id = e.id
			ORDER BY d.created_at DESC, d.id DESC
			LIMIT 1
		) latest_delivery ON true
		WHERE e.id = $1`, event.ID).Scan(&runtimeID)
	return runtimeID
}

func agentInboxActivityTarget(event db.AgentInboxEvent) (string, pgtype.UUID) {
	if event.ChannelID.Valid {
		return "channel", event.ChannelID
	}
	if event.ChatSessionID.Valid {
		return "dm", event.ChatSessionID
	}
	return "agent", event.AgentID
}

func (h *Handler) recordAgentInboxStatusActivity(ctx context.Context, event db.AgentInboxEvent, runtimeID, deliveryID pgtype.UUID, status string) {
	if h == nil || h.DB == nil || !event.RequiresWake {
		return
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return
	}
	// The wake/source event already marks the start of work in the Activity
	// timeline (for example, "Working · Message received"). Do not add a
	// second generic "Working" row for the same transition.
	if status == agentInboxStatusActivityWorking {
		return
	}
	if h.agentInboxStatusActivityExists(ctx, event.WorkspaceID, event.AgentID, event.ID, status) {
		return
	}
	targetKind, targetID := agentInboxActivityTarget(event)
	details := map[string]any{
		"status":            status,
		"inbox_event_id":    uuidToString(event.ID),
		"source_message_id": uuidToString(event.SourceMessageID),
	}
	if deliveryID.Valid {
		details["delivery_id"] = uuidToString(deliveryID)
	}
	h.recordAgentActivityEvent(ctx, h.DB,
		event.WorkspaceID, event.AgentID, runtimeID, pgtype.UUID{},
		activityKindCustom, agentInboxStatusChangedEventType, "info",
		targetKind, targetID, "",
		"", agentInboxStatusActivityMessage(status),
		details,
	)
}

func (h *Handler) agentInboxStatusActivityExists(ctx context.Context, workspaceID, agentID, inboxEventID pgtype.UUID, status string) bool {
	if h == nil || h.DB == nil {
		return false
	}
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_activity_event
			WHERE workspace_id = $1
			  AND agent_id = $2
			  AND event_kind = $3
			  AND event_type = $4
			  AND details->>'inbox_event_id' = $5
			  AND COALESCE(details->>'status', '') = $6
		)
	`, workspaceID, agentID, activityKindCustom, agentInboxStatusChangedEventType, uuidToString(inboxEventID), status).Scan(&exists)
	if err != nil {
		slog.Warn("agent inbox status activity: duplicate lookup failed", "agent_id", uuidToString(agentID), "inbox_event_id", uuidToString(inboxEventID), "status", status, "error", err)
		return false
	}
	return exists
}

func agentInboxStatusActivityMessage(status string) string {
	switch status {
	case agentInboxStatusActivityWorking:
		return "Working"
	case agentInboxStatusActivityIdle:
		return "Idle"
	default:
		return strings.TrimSpace(status)
	}
}

func agentInboxTaskMessagePayload(event db.AgentInboxEvent, msg TaskMessageRequest, kind string, details map[string]any) (protocol.TaskMessagePayload, bool) {
	payload := protocol.TaskMessagePayload{
		TaskID:     uuidToString(event.ID),
		Seq:        msg.Seq,
		Visibility: "user_facing",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	switch kind {
	case activityKindThinking:
		payload.Type = "thinking"
		payload.Content = agentInboxActivityMessageText(msg)
	case activityKindToolCall:
		payload.Type = "tool_use"
		payload.Tool = stringFromMap(details, "tool")
		if payload.Tool == "" {
			return protocol.TaskMessagePayload{}, false
		}
		payload.Input = msg.Input
	case activityKindError:
		payload.Type = "error"
		payload.Content = agentInboxActivityMessageText(msg)
	default:
		return protocol.TaskMessagePayload{}, false
	}
	return payload, true
}

func agentInboxActivityMessageKind(messageType string) (kind, eventType, severity string) {
	switch messageType {
	case "thinking":
		return activityKindThinking, "thinking", "info"
	case "tool_use":
		return activityKindToolCall, "tool_use", "info"
	case "tool_result":
		return activityKindToolOutput, "tool_result", "info"
	case "error":
		return activityKindError, "error", "error"
	case "log":
		return activityKindCustom, "runtime_text", "info"
	case "text":
		// For inbox/direct runs, streaming text is not necessarily a user-visible
		// reply: several runtimes emit wrapper logs or plan narration on this
		// channel. Visible replies get their own message_sent event on completion
		// or transport send, so keep raw runtime text diagnostic-only.
		return activityKindCustom, "runtime_text", "info"
	default:
		return activityKindCustom, "runtime_message", "info"
	}
}

func agentInboxActivityMessageText(msg TaskMessageRequest) string {
	switch msg.Type {
	case "thinking", "text", "error", "log":
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			text = strings.TrimSpace(msg.Output)
		}
		return truncateForActivity(text, 500)
	case "tool_result":
		return ""
	default:
		return ""
	}
}

func agentInboxActivityToolTarget(msg TaskMessageRequest) (string, string) {
	if msg.Type != "tool_use" {
		return "", ""
	}
	canonicalTool, known := taskMessageCanonicalToolName(msg.Tool, msg.Input)
	if !known {
		canonicalTool = ""
	}
	return agentActivitySafeToolTargetForTool(canonicalTool, msg.Input)
}

func (h *Handler) recordAgentInboxVisibleOutputActivity(ctx context.Context, event db.AgentInboxEvent, runtimeID pgtype.UUID, payload protocol.ChatDonePayload) {
	if payload.Type != protocol.ChatOutputKindMessage {
		return
	}
	if strings.TrimSpace(payload.OutputSuppressedReason) != "" {
		return
	}
	if strings.TrimSpace(payload.Content) == "" && len(payload.Parts) == 0 && strings.TrimSpace(payload.MessageID) == "" {
		return
	}
	targetKind, targetID := agentInboxActivityTarget(event)
	if len(payload.Parts) == 0 && outputClaimsFileDelivery(payload.Content) {
		h.recordMissingArtifactActivity(ctx, event.WorkspaceID, event.AgentID, runtimeID, pgtype.UUID{}, targetKind, targetID, "", map[string]any{
			"inbox_event_id":    uuidToString(event.ID),
			"source_message_id": uuidToString(event.SourceMessageID),
			"message_id":        strings.TrimSpace(payload.MessageID),
		})
		return
	}
	details := map[string]any{
		"inbox_event_id":    uuidToString(event.ID),
		"source_message_id": uuidToString(event.SourceMessageID),
		"created":           true,
	}
	if strings.TrimSpace(payload.MessageID) != "" {
		details["message_id"] = strings.TrimSpace(payload.MessageID)
	}
	if strings.TrimSpace(payload.Target) != "" {
		details["target"] = strings.TrimSpace(payload.Target)
	}
	if len(payload.Parts) > 0 {
		details["parts_count"] = len(payload.Parts)
	}
	h.recordAgentActivityEvent(ctx, h.DB,
		event.WorkspaceID, event.AgentID, runtimeID, pgtype.UUID{},
		activityKindText, "message_sent", "info",
		targetKind, targetID, "",
		"", agentVisibleOutputActivityText(payload.Content, payload.Parts, nil),
		details,
	)
}

func (h *Handler) recordTaskVisibleOutputActivity(ctx context.Context, workspaceID pgtype.UUID, task db.AgentTaskQueue, req TaskCompleteRequest) {
	if req.Type != protocol.ChatOutputKindMessage {
		return
	}
	if strings.TrimSpace(req.OutputSuppressedReason) != "" {
		return
	}
	if strings.TrimSpace(req.Output) == "" && len(req.Parts) == 0 {
		return
	}
	targetKind, targetID, targetSlug := h.taskActivityTarget(ctx, task)
	if len(req.Parts) == 0 && outputClaimsFileDelivery(req.Output) {
		h.recordMissingArtifactActivity(ctx, workspaceID, task.AgentID, task.RuntimeID, task.ID, targetKind, targetID, targetSlug, map[string]any{
			"target": strings.TrimSpace(req.Target),
		})
		return
	}
	details := map[string]any{
		"created": true,
	}
	if strings.TrimSpace(req.Target) != "" {
		details["target"] = strings.TrimSpace(req.Target)
	}
	if len(req.Parts) > 0 {
		details["parts_count"] = len(req.Parts)
	}
	h.recordAgentActivityEvent(ctx, h.DB,
		workspaceID, task.AgentID, task.RuntimeID, task.ID,
		activityKindText, "message_sent", "info",
		targetKind, targetID, targetSlug,
		"", agentVisibleOutputActivityText(req.Output, req.Parts, nil),
		details,
	)
}

func (h *Handler) recordMissingArtifactActivity(ctx context.Context, workspaceID, agentID, runtimeID, taskID pgtype.UUID, targetKind string, targetID pgtype.UUID, targetSlug string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	details["artifact_consistency"] = "missing_attachment"
	h.recordAgentActivityEvent(ctx, h.DB,
		workspaceID, agentID, runtimeID, taskID,
		activityKindError, "artifact_missing", "error",
		targetKind, targetID, targetSlug,
		"missing_file_attachment", "Agent claimed to send a file, but no attachment was produced",
		details,
	)
}

func (h *Handler) taskActivityTarget(ctx context.Context, task db.AgentTaskQueue) (string, pgtype.UUID, string) {
	if task.IssueID.Valid {
		return "issue", task.IssueID, ""
	}
	if task.ChatSessionID.Valid {
		if channelID := h.channelIDForChatSession(ctx, task.ChatSessionID); channelID != "" {
			if root := h.threadRootIDForChatSession(ctx, task.ChatSessionID); root != nil {
				return "thread", parseUUID(*root), channelID
			}
			return "channel", parseUUID(channelID), ""
		}
		return "dm", task.ChatSessionID, ""
	}
	return "agent", task.AgentID, ""
}

func outputClaimsFileDelivery(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || !claimedFileDeliveryRe.MatchString(trimmed) {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{
		"attached",
		"attachment",
		"created",
		"generated",
		"saved",
		"sent",
		"sending",
		"written",
		"wrote",
		"here is",
		"here's",
		"给你",
		"发给你",
		"创建了",
		"已创建",
		"生成了",
		"已生成",
		"保存了",
		"已保存",
		"写好了",
		"已发送",
		"附件",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func agentVisibleOutputActivityText(content string, parts []protocol.MessagePart, attachments []AttachmentResponse) string {
	text := strings.TrimSpace(redact.Text(content))
	if text != "" {
		return truncateForActivity(text, 500)
	}
	text = strings.TrimSpace(redact.Text(messageparts.FallbackContent(parts)))
	if text != "" {
		return truncateForActivity(text, 500)
	}
	switch len(attachments) {
	case 0:
	case 1:
		filename := strings.TrimSpace(attachments[0].Filename)
		if filename != "" {
			return truncateForActivity("Sent attachment: "+filename, 500)
		}
		return "Sent an attachment"
	default:
		return fmt.Sprintf("Sent %d attachments", len(attachments))
	}
	return "Sent a message"
}

func (h *Handler) populateAgentInboxChatContext(ctx context.Context, event db.AgentInboxEvent, resp *AgentTaskResponse) bool {
	if !event.ChatSessionID.Valid {
		return false
	}
	cs, err := h.Queries.GetChatSession(ctx, event.ChatSessionID)
	if err != nil {
		return false
	}
	resp.WorkspaceID = uuidToString(cs.WorkspaceID)
	resp.ChatSessionID = uuidToString(cs.ID)
	resp.ThreadName = cs.Title

	var chatProjectID pgtype.UUID
	_ = h.DB.QueryRow(ctx, `
		SELECT COALESCE(ch.project_id, cs.project_id)
		FROM chat_session cs
		LEFT JOIN channel_agent_session cas ON cas.chat_session_id = cs.id
		LEFT JOIN channel ch ON ch.id = cas.channel_id
		WHERE cs.id = $1`, cs.ID).Scan(&chatProjectID)
	if chatProjectID.Valid {
		resp.ProjectID = uuidToString(chatProjectID)
		if proj, err := h.Queries.GetProject(ctx, chatProjectID); err == nil {
			resp.ProjectTitle = proj.Title
		}
		resources, projectRepos := h.mapProjectResources(ctx, chatProjectID)
		if len(resources) > 0 {
			resp.ProjectResources = resources
			if len(projectRepos) > 0 {
				resp.Repos = projectRepos
			}
		} else {
			resp.ProvisionManagedWorkdir = true
			resp.ManagedWorkdirRelPath = managedWorkdirRelPath(resp.ProjectID)
		}
	}
	if len(resp.Repos) == 0 && !resp.ProvisionManagedWorkdir {
		if ws, err := h.Queries.GetWorkspace(ctx, cs.WorkspaceID); err == nil && ws.Repos != nil {
			var repos []RepoData
			if json.Unmarshal(ws.Repos, &repos) == nil && len(repos) > 0 {
				resp.Repos = repos
			}
		}
	}
	runtimeMatches := func(runtimeID pgtype.UUID) bool {
		return runtimeID.Valid && resp.RuntimeID != "" && uuidToString(runtimeID) == resp.RuntimeID
	}
	if runtimeMatches(cs.RuntimeID) {
		if cs.SessionID.Valid {
			resp.PriorSessionID = cs.SessionID.String
		}
		if cs.WorkDir.Valid {
			resp.PriorWorkDir = cs.WorkDir.String
		}
	}
	if prior, err := h.Queries.GetLastChatTaskSession(ctx, cs.ID); err == nil && prior.SessionID.Valid && runtimeMatches(prior.RuntimeID) {
		if resp.PriorSessionID == "" {
			resp.PriorSessionID = prior.SessionID.String
		}
		if prior.WorkDir.Valid && resp.PriorWorkDir == "" {
			resp.PriorWorkDir = prior.WorkDir.String
		}
	}
	seenAttachmentIDs := make(map[string]struct{}, len(resp.ChatMessageAttachments))
	for _, attachment := range resp.ChatMessageAttachments {
		seenAttachmentIDs[attachment.ID] = struct{}{}
	}
	appendAttachments := func(attachments []db.Attachment) {
		for _, attachment := range attachments {
			id := uuidToString(attachment.ID)
			if _, exists := seenAttachmentIDs[id]; exists {
				continue
			}
			seenAttachmentIDs[id] = struct{}{}
			resp.ChatMessageAttachments = append(resp.ChatMessageAttachments, ChatAttachmentMeta{
				ID:          id,
				Filename:    attachment.Filename,
				ContentType: attachment.ContentType,
			})
		}
	}
	if event.SourceMessageID.Valid {
		h.populateAgentInboxInitiator(ctx, event.SourceMessageID, resp)
		if atts, attErr := h.Queries.ListAttachmentsByChannelMessageIDs(ctx, db.ListAttachmentsByChannelMessageIDsParams{
			Column1:     []pgtype.UUID{event.SourceMessageID},
			WorkspaceID: cs.WorkspaceID,
		}); attErr == nil {
			appendAttachments(atts)
		}
	}
	if msgs, err := h.Queries.ListChatMessages(ctx, cs.ID); err == nil && len(msgs) > 0 {
		promptMessages := inboxPromptMessages(msgs, event.ID)
		if len(promptMessages) == 0 {
			return false
		}
		parts := make([]string, 0, len(promptMessages))
		for _, m := range promptMessages {
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, m.Content)
			}
			if atts, attErr := h.Queries.ListAttachmentsByChatMessage(ctx, db.ListAttachmentsByChatMessageParams{
				ChatMessageID: m.ID,
				WorkspaceID:   cs.WorkspaceID,
			}); attErr == nil && len(atts) > 0 {
				appendAttachments(atts)
			}
		}
		resp.ChatMessage = strings.Join(parts, "\n\n")
		if strings.TrimSpace(resp.ThreadName) == "" {
			resp.ThreadName = resp.ChatMessage
		}
		if len(msgs) > 0 {
			totalTokens := h.chatSessionTokenTotal(ctx, cs.ID)
			channelID := h.channelIDForChatSession(ctx, cs.ID)
			threadRootID := h.threadRootIDForChatSession(ctx, cs.ID)
			surface := buildConversationSurface(resp.WorkspaceID, uuidToString(event.AgentID), cs.ID, channelID, threadRootID, "")
			if shouldIncludeChatContextSummary(msgs) {
				resp.ChatContextSummary = buildChatContextSummary(msgs, totalTokens, "", resp.WorkspaceID, uuidToString(event.AgentID), surface)
			}
		}
		return true
	}
	return false
}

func (h *Handler) populateAgentInboxInitiator(ctx context.Context, sourceMessageID pgtype.UUID, resp *AgentTaskResponse) {
	var authorType string
	var authorID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `
		SELECT author_type, author_id
		FROM channel_message
		WHERE id = $1`, sourceMessageID).Scan(&authorType, &authorID); err != nil {
		return
	}
	resp.InitiatorType = authorType
	if authorID.Valid {
		resp.InitiatorID = uuidToString(authorID)
	}
	switch authorType {
	case "agent":
		if authorID.Valid {
			if a, err := h.Queries.GetAgent(ctx, authorID); err == nil {
				resp.InitiatorName = agentDisplayName(a)
			}
		}
	case "member", "user":
		resp.InitiatorType = "member"
		if authorID.Valid {
			if u, err := h.Queries.GetUser(ctx, authorID); err == nil {
				resp.InitiatorName = userDisplayName(u)
				resp.InitiatorEmail = u.Email
			}
		}
	}
}

func (h *Handler) completedAgentInboxChatPayload(ctx context.Context, q *db.Queries, event db.AgentInboxEvent, req TaskCompleteRequest) (*protocol.ChatDonePayload, error) {
	if !event.ChatSessionID.Valid {
		return nil, nil
	}
	var msg *db.ChatMessage
	outputType := req.Type
	body := util.UnescapeBackslashEscapes(req.Output)
	parts := req.Parts
	if unwrappedBody, unwrappedParts, unwrapped, unwrapErr := messageparts.UnwrapStructuredMessageSend(body, parts); unwrapErr != nil {
		if unwrapped {
			slog.Warn("agent inbox complete: dropping invalid structured assistant chat output", "inbox_event_id", uuidToString(event.ID), "error", unwrapErr)
			body = ""
			parts = nil
		}
	} else if unwrapped {
		body = unwrappedBody
		parts = unwrappedParts
	}
	var partsErr error
	body, parts, partsErr = messageparts.Normalize(body, parts)
	if partsErr != nil {
		slog.Warn("agent inbox complete: dropping invalid chat message parts", "inbox_event_id", uuidToString(event.ID), "error", partsErr)
		parts = nil
	}
	if outputType == "" {
		normalized, err := protocol.NormalizeChatOutputType(req.Type, strings.TrimSpace(body) != "" || len(parts) > 0, req.Reaction != nil)
		if err == nil {
			outputType = normalized
		}
	}
	if (outputType == protocol.ChatOutputKindMessage || outputType == protocol.ChatOutputKindReaction) && h.inboxEventHasAgentTransportVisibleOutput(ctx, event.ID) {
		outputType = protocol.ChatOutputKindNoReply
		body = ""
		parts = nil
		req.OutputSuppressedReason = protocol.ChannelOutputSuppressedReasonToolTransportOutput
	}
	visibleContent := ""
	var visibleParts []protocol.MessagePart
	if outputType == protocol.ChatOutputKindMessage && strings.TrimSpace(req.Target) == "" && strings.TrimSpace(body) != "" {
		row, err := q.CreateChatMessage(ctx, db.CreateChatMessageParams{
			ChatSessionID: event.ChatSessionID,
			Role:          "assistant",
			Content:       redact.Text(body),
			Parts:         messageparts.MustJSON(parts),
			TaskID:        event.ID,
		})
		if err != nil {
			slog.Error("agent inbox complete: failed to save assistant chat message", "inbox_event_id", uuidToString(event.ID), "error", err)
			return nil, err
		} else {
			msg = &row
			if err := q.SetUnreadSinceIfNull(ctx, event.ChatSessionID); err != nil {
				slog.Warn("agent inbox complete: failed to set unread_since", "chat_session_id", uuidToString(event.ChatSessionID), "error", err)
			}
		}
	} else if outputType == protocol.ChatOutputKindMessage {
		visibleContent = redact.Text(body)
		visibleParts = parts
	}
	payload := protocol.ChatDonePayload{
		ChatSessionID:          uuidToString(event.ChatSessionID),
		TaskID:                 uuidToString(event.ID),
		Type:                   outputType,
		Target:                 strings.TrimSpace(req.Target),
		Options:                req.Options,
		Reaction:               req.Reaction,
		OutputSuppressedReason: req.OutputSuppressedReason,
		Content:                visibleContent,
		Parts:                  visibleParts,
	}
	if msg != nil {
		payload.Type = protocol.ChatOutputKindMessage
		payload.Reaction = nil
		payload.MessageID = uuidToString(msg.ID)
		payload.Content = msg.Content
		payload.Parts = messageparts.Decode(msg.Parts)
		if msg.CreatedAt.Valid {
			payload.CreatedAt = msg.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		}
	}
	return &payload, nil
}

func (h *Handler) publishAgentInboxChatDone(event db.AgentInboxEvent, payload protocol.ChatDonePayload) {
	if h.Bus != nil {
		h.Bus.Publish(events.Event{
			Type:          protocol.EventChatDone,
			WorkspaceID:   uuidToString(event.WorkspaceID),
			ActorType:     "system",
			ChatSessionID: uuidToString(event.ChatSessionID),
			Payload:       payload,
		})
	}
}

func (h *Handler) requireDaemonInboxEventAccess(w http.ResponseWriter, r *http.Request, eventID pgtype.UUID) (db.AgentInboxEvent, bool) {
	event, err := h.Queries.GetAgentInboxEvent(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return db.AgentInboxEvent{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load inbox event")
		return db.AgentInboxEvent{}, false
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(event.WorkspaceID)) {
		return db.AgentInboxEvent{}, false
	}
	return event, true
}
