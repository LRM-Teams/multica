package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

const (
	agentInboxDrainMessageLimit      = 50
	agentInboxStatusChangedEventType = "agent_status_changed"
)

type DrainAgentInboxResponse struct {
	Events      []AgentInboxEventResponse `json:"events"`
	LastSeenSeq int64                     `json:"last_seen_seq"`
	HasMore     bool                      `json:"has_more"`
}

type AgentInboxEventResponse struct {
	ID               string                   `json:"id"`
	DeliveryID       string                   `json:"delivery_id"`
	AgentSessionID   string                   `json:"agent_session_id"`
	ConversationID   string                   `json:"conversation_id"`
	ChannelID        string                   `json:"channel_id,omitempty"`
	ChatSessionID    string                   `json:"chat_session_id,omitempty"`
	AgentID          string                   `json:"agent_id"`
	SourceMessageID  string                   `json:"source_message_id,omitempty"`
	Reason           string                   `json:"reason"`
	DeliveryMode     string                   `json:"delivery_mode"`
	ResponseMode     string                   `json:"response_mode"`
	ExecutionProfile string                   `json:"execution_profile"`
	RequiresWake     bool                     `json:"requires_wake"`
	Priority         int32                    `json:"priority"`
	SeqFrom          int64                    `json:"seq_from"`
	SeqTo            int64                    `json:"seq_to"`
	Messages         []ChannelMessageResponse `json:"messages,omitempty"`
	LeaseToken       string                   `json:"lease_token"`
	LeaseExpiresAt   string                   `json:"lease_expires_at"`
	Task             *AgentTaskResponse       `json:"task,omitempty"`
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

// AgentInboxExecutionRequest is deliberately separate from a delivery. A
// delivery is a renewable transport lease; execution_id identifies one actual
// provider run and is minted by the daemon immediately before that run starts.
type AgentInboxExecutionRequest struct {
	DeliveryID  string `json:"delivery_id"`
	LeaseToken  string `json:"lease_token"`
	ExecutionID string `json:"execution_id"`
}

type ReportAgentInboxUsageRequest struct {
	DeliveryID  string              `json:"delivery_id"`
	LeaseToken  string              `json:"lease_token"`
	ExecutionID string              `json:"execution_id"`
	Usage       []AgentUsagePayload `json:"usage"`
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
	DeliveryID  string              `json:"delivery_id"`
	LeaseToken  string              `json:"lease_token"`
	ExecutionID string              `json:"execution_id,omitempty"`
	Usage       []AgentUsagePayload `json:"usage,omitempty"`
	TaskCompleteRequest
}

// isLegacyChatInboxEvent identifies residual task-shaped channel/DM chat
// wakes that the MessageCoordinator owns after the #2295 hard-cut.
// Standalone FAB/bubble chat_session tasks (reason=dm, no channel_id) are a
// retained product surface and continue to drain/execute through agent_inbox.
func isLegacyChatInboxEvent(event db.AgentInboxEvent) bool {
	switch strings.TrimSpace(event.Reason) {
	case "channel_message", "thread_reply", "ambient", "mention":
		return true
	case "dm":
		// Only channel-scoped DM agent wakes are cut over. Standalone bubble
		// chat keeps reason=dm without channel_id.
		return event.ChannelID.Valid
	default:
		return false
	}
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
	if err := h.materializeNextChannelOnboardingForRuntime(r.Context(), runtime); err != nil {
		slog.Warn("agent inbox drain: failed to materialize channel onboarding", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to materialize channel onboarding")
		return
	}
	// Turn-fold: return all pending runnable events for ONE conversation so the
	// daemon never parks a different-conversation lease (Alice boundary #1).
	deliveries, err := h.leaseAgentInboxConversationBatchForRuntime(r.Context(), runtime, agentInboxConversationFoldMax)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, DrainAgentInboxResponse{Events: []AgentInboxEventResponse{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to drain agent inbox")
		return
	}
	if len(deliveries) == 0 {
		writeJSON(w, http.StatusOK, DrainAgentInboxResponse{Events: []AgentInboxEventResponse{}})
		return
	}

	respEvents := make([]AgentInboxEventResponse, 0, len(deliveries))
	var lastSeenSeq int64
	var invalidContextCount int
	for _, delivery := range deliveries {
		event, err := h.Queries.GetAgentInboxEvent(r.Context(), delivery.InboxEventID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load inbox event")
			return
		}
		if event.WorkspaceID != runtime.WorkspaceID {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		// #2295/#2296: residual task-shaped channel/DM chat wakes must not
		// execute. Ordinary channel chat is MessageCoordinator-only. Product
		// tasks and retained standalone FAB/bubble chat_session (dm without
		// channel_id) still drain normally.
		if isLegacyChatInboxEvent(event) {
			_, _ = h.DB.Exec(r.Context(), `
				UPDATE agent_event_delivery
				SET status = 'failed',
				    last_error = 'legacy chat inbox path removed; use canonical Message delivery',
				    updated_at = now()
				WHERE id = $1
				  AND status IN ('leased', 'processing')`, delivery.ID)
			_, _ = h.DB.Exec(r.Context(), `
				UPDATE agent_inbox_event
				SET status = 'suppressed',
				    terminal_outcome = 'cancelled',
				    terminal_delivery_id = $2,
				    terminal_at = now(),
				    completed_at = COALESCE(completed_at, now()),
				    last_error = 'legacy chat inbox path removed; use canonical Message delivery',
				    updated_at = now()
				WHERE id = $1
				  AND status = 'draining'`, event.ID, delivery.ID)
			slog.Info("suppressed residual legacy chat inbox event on drain",
				"runtime_id", runtimeID,
				"event_id", uuidToString(event.ID),
				"reason", event.Reason,
			)
			continue
		}
		respEvent := h.agentInboxEventResponse(r.Context(), runtime, event, delivery)
		if event.RequiresWake && respEvent.Task == nil {
			_, _ = h.DB.Exec(r.Context(), `
				UPDATE agent_event_delivery
				SET status = 'failed',
				    last_error = 'invalid inbox event execution context',
				    updated_at = now()
				WHERE id = $1
				  AND status IN ('leased', 'processing')`, delivery.ID)
			_, _ = h.DB.Exec(r.Context(), `
				UPDATE agent_inbox_event
				SET status = 'suppressed',
				    terminal_outcome = 'cancelled',
				    terminal_delivery_id = $2,
				    terminal_at = now(),
				    completed_at = COALESCE(completed_at, now()),
				    last_error = 'invalid inbox event execution context',
				    updated_at = now()
				WHERE id = $1
				  AND status = 'draining'`, event.ID, delivery.ID)
			invalidContextCount++
			continue
		}
		h.publishAgentInboxTaskLifecycle(protocol.EventTaskDispatch, event, runtime.ID, "running")
		if event.SeqTo > lastSeenSeq {
			lastSeenSeq = event.SeqTo
		}
		respEvents = append(respEvents, respEvent)
	}
	if len(respEvents) == 0 {
		if invalidContextCount > 0 {
			writeError(w, http.StatusInternalServerError, "inbox event has invalid execution context")
			return
		}
		writeJSON(w, http.StatusOK, DrainAgentInboxResponse{Events: []AgentInboxEventResponse{}})
		return
	}
	// Leased events are draining, so this counts other conversations (and any
	// same-conversation events beyond the fold cap) still pending.
	pending, _ := h.countReadyAgentInboxEventsForRuntime(r.Context(), runtime)
	writeJSON(w, http.StatusOK, DrainAgentInboxResponse{
		Events:      respEvents,
		LastSeenSeq: lastSeenSeq,
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
	_, ok = h.requireActiveAgentInboxDelivery(w, r, event, deliveryID, leaseToken)
	if !ok {
		return
	}
	for _, msg := range req.Messages {
		msg.Content = redact.Text(msg.Content)
		msg.Output = redact.Text(msg.Output)
		msg.Input = redact.InputMap(msg.Input)
		if canonicalTool, known := taskMessageCanonicalToolName(msg.Tool, msg.Input); known {
			msg.Tool = canonicalTool
		}
		if taskMessageRequestVisibility(msg) != "user_facing" {
			continue
		}
		details := map[string]any{
			"inbox_event_id":    uuidToString(event.ID),
			"delivery_id":       uuidToString(deliveryID),
			"source_message_id": uuidToString(event.SourceMessageID),
			"seq":               msg.Seq,
		}
		if lineage := strings.TrimSpace(msg.Lineage); lineage != "" {
			details["lineage"] = lineage
		}
		if taskMessageIsPhaseStatus(msg.Type, msg.Content) {
			// Legacy daemons may still report an empty thinking phase. Retain it
			// as diagnostic data only; current daemons no longer emit this wire.
			details["phase_status"] = true
			if payload, ok := agentInboxTaskMessagePayload(event, msg, "thinking", details); ok && h.Bus != nil {
				h.publishTask(protocol.EventTaskMessage, uuidToString(event.WorkspaceID), "system", "", uuidToString(event.ID), payload)
			}
			continue
		}
		kind, _, _ := agentInboxActivityMessageKind(msg.Type)
		if callID := strings.TrimSpace(msg.CallID); callID != "" {
			details["call_id"] = callID
		}
		if msg.Type == "tool_use" {
			tool := strings.TrimSpace(msg.Tool)
			if tool == "" {
				continue
			}
			details["tool"] = tool
		}
		if payload, ok := agentInboxTaskMessagePayload(event, msg, kind, details); ok && h.Bus != nil {
			h.publishTask(protocol.EventTaskMessage, uuidToString(event.WorkspaceID), "system", "", uuidToString(event.ID), payload)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// StartAgentInboxExecution persists immutable attribution before the daemon
// calls a provider. Reporting usage afterwards may be retried, but it cannot
// manufacture a provider run or infer a runtime from the current session.
func (h *Handler) StartAgentInboxExecution(w http.ResponseWriter, r *http.Request) {
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "event_id")
	if !ok {
		return
	}
	event, ok := h.requireDaemonInboxEventAccess(w, r, eventID)
	if !ok {
		return
	}
	var req AgentInboxExecutionRequest
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
	executionID, ok := parseUUIDOrBadRequest(w, req.ExecutionID, "execution_id")
	if !ok {
		return
	}
	if _, err := h.TaskService.StartAgentInboxTask(r.Context(), executionID, service.AgentInboxDeliveryFence{
		DeliveryID:   deliveryID,
		InboxEventID: event.ID,
		LeaseToken:   leaseToken,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active or execution_id is already bound")
			return
		}
		slog.Warn("start inbox execution failed", "execution_id", req.ExecutionID, "inbox_event_id", uuidToString(event.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start inbox execution")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ReportAgentInboxUsage(w http.ResponseWriter, r *http.Request) {
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "event_id")
	if !ok {
		return
	}
	event, ok := h.requireDaemonInboxEventAccess(w, r, eventID)
	if !ok {
		return
	}
	var req ReportAgentInboxUsageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// The execution was authenticated and persisted while its delivery was
	// active. A provider may finish after that delivery expires or is reclaimed,
	// so retries are authorized by the immutable execution/event binding below,
	// not by mutable transport ownership.
	executionID, ok := parseUUIDOrBadRequest(w, req.ExecutionID, "execution_id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetAgentInboxExecution(r.Context(), db.GetAgentInboxExecutionParams{
		ExecutionID:  executionID,
		InboxEventID: event.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "provider execution was not started")
			return
		}
		slog.Warn("load inbox execution failed", "execution_id", req.ExecutionID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load inbox execution")
		return
	}
	source := "issue"
	if event.ChatSessionID.Valid {
		source = "chat"
	}
	for _, u := range req.Usage {
		if err := h.Queries.UpsertAgentUsage(r.Context(), db.UpsertAgentUsageParams{
			ExecutionID:      executionID,
			Source:           source,
			Provider:         u.Provider,
			Model:            u.Model,
			InputTokens:      u.InputTokens,
			OutputTokens:     u.OutputTokens,
			CacheReadTokens:  u.CacheReadTokens,
			CacheWriteTokens: u.CacheWriteTokens,
		}); err != nil {
			slog.Warn("upsert inbox agent usage failed", "execution_id", req.ExecutionID, "model", u.Model, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to record inbox usage")
			return
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
	result, err := json.Marshal(req.TaskCompleteRequest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode inbox completion")
		return
	}
	var executionID pgtype.UUID
	if strings.TrimSpace(req.ExecutionID) != "" {
		executionID, ok = parseUUIDOrBadRequest(w, req.ExecutionID, "execution_id")
		if !ok {
			return
		}
	}

	var workCompletion *service.CompleteTaskOutcome
	ackedSeq := int64(0)
	isChannelOnboarding := event.Reason == channelOnboardingReason
	var onboardingID pgtype.UUID
	channelOnboardingActive := false
	var chatDonePayload *protocol.ChatDonePayload
	terminalOutcome := ""
	var collaborationWakes []channelAgentWake
	if !event.ChatSessionID.Valid {
		workCompletion, err = h.TaskService.CompleteDaemonInboxTaskWithFinalization(
			r.Context(),
			service.AgentInboxDeliveryFence{
				DeliveryID:   deliveryID,
				InboxEventID: event.ID,
				LeaseToken:   leaseToken,
			},
			result,
			req.SessionID,
			req.WorkDir,
			service.AgentInboxCompleteTxHooks{
				Before: func(_ *db.Queries, tx pgx.Tx) error {
					if isChannelOnboarding {
						onboardingID, err = channelOnboardingIDForInboxEventTx(r.Context(), tx, event.ID)
						if err != nil {
							return fmt.Errorf("load channel onboarding: %w", err)
						}
						if !onboardingID.Valid {
							return errors.New("channel onboarding inbox event is missing its canonical event")
						}
						// Eligibility must be locked before the delivery row. The
						// membership DELETE trigger owns membership first and then
						// expires deliveries, so the reverse order can deadlock.
						channelOnboardingActive, err = channelOnboardingGenerationActiveTx(
							r.Context(), tx, onboardingID, event.ChannelID, event.AgentID, true,
						)
						if err != nil {
							return err
						}
					}
					return nil
				},
				After: func(qtx *db.Queries, tx pgx.Tx, outcome *service.CompleteTaskOutcome) error {
					event = outcome.Task
					task = agentInboxSyntheticTask(event, h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID))
					if err := persistAgentInboxCompletionTx(r.Context(), tx, event.ID, result, req.SessionID, req.WorkDir); err != nil {
						return err
					}
					event.Result = result
					event.SessionID = strToText(req.SessionID)
					event.WorkDir = strToText(req.WorkDir)

					if isChannelOnboarding {
						terminalOutcome, err = h.completeChannelOnboardingTx(
							r.Context(), tx, event, deliveryID, onboardingID, channelOnboardingActive, req.ChannelOnboardingDecision,
						)
						if err != nil {
							return err
						}
					} else if !event.IssueID.Valid && (event.ChannelID.Valid || channelOnlyWakeReason(event.Reason)) {
						// LRM-1079: channel-only wakes have no chat_message prompt /
						// ChatDone bridge; terminal outcome follows transport + action.
						// Issue/work tasks (even if they carry a channel_id) keep
						// the historical terminal ("completed").
						terminalOutcome = agentInboxCompletionTerminalOutcome(
							r.Context(), h, event, req.TaskCompleteRequest, nil,
						)
					} else {
						terminalOutcome = "completed"
					}
					if err := setAgentInboxCompletionFinalizationTx(
						r.Context(), qtx, tx, event, deliveryID, executionID, result, terminalOutcome,
					); err != nil {
						return err
					}
					collaborationWakes, err = h.completeCollaborationTurnTx(r.Context(), tx, event)
					return err
				},
			},
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errChannelOnboardingDecisionRequired) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to finalize inbox completion")
			return
		}
		ackedSeq = workCompletion.AckedSeq
		event = workCompletion.Task
		task = agentInboxSyntheticTask(event, h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID))
		if event.ChannelID.Valid {
			h.publishChannelTypingStopForInboxEvent(r.Context(), event)
		}
	} else {
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

		if isChannelOnboarding {
			onboardingID, err = channelOnboardingIDForInboxEventTx(r.Context(), tx, event.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load channel onboarding")
				return
			}
			if !onboardingID.Valid {
				writeError(w, http.StatusInternalServerError, "channel onboarding inbox event is missing its canonical event")
				return
			}
			channelOnboardingActive, err = channelOnboardingGenerationActiveTx(
				r.Context(), tx, onboardingID, event.ChannelID, event.AgentID, true,
			)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to lock channel onboarding eligibility")
				return
			}
		}

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
		ackedSeq = acked.SeqTo
		if err := persistAgentInboxCompletionTx(r.Context(), tx, event.ID, result, req.SessionID, req.WorkDir); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist inbox completion")
			return
		}
		event.Result = result
		event.SessionID = strToText(req.SessionID)
		event.WorkDir = strToText(req.WorkDir)

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
		if !isChannelOnboarding {
			chatDonePayload, err = h.completedAgentInboxChatPayload(r.Context(), qtx, event, req.TaskCompleteRequest)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to save inbox chat output")
				return
			}
			terminalOutcome = agentInboxCompletionTerminalOutcome(r.Context(), h, event, req.TaskCompleteRequest, chatDonePayload)
		} else {
			terminalOutcome, err = h.completeChannelOnboardingTx(
				r.Context(), tx, event, deliveryID, onboardingID, channelOnboardingActive, req.ChannelOnboardingDecision,
			)
			if err != nil {
				if errors.Is(err, errChannelOnboardingDecisionRequired) {
					writeError(w, http.StatusConflict, err.Error())
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to complete channel onboarding")
				return
			}
		}
		if err := setAgentInboxCompletionFinalizationTx(
			r.Context(), qtx, tx, event, deliveryID, executionID, result, terminalOutcome,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusConflict, "provider execution is no longer active")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to finalize inbox completion")
			return
		}
		collaborationWakes, err = h.completeCollaborationTurnTx(r.Context(), tx, event)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to advance collaboration turn")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit inbox completion")
			return
		}
		// The chat-session branch acks the delivery itself and never reaches
		// completeTask, so it owes the terminal side effects the work-task
		// branch gets for free. Rollout agents always run in a chat session.
		h.TaskService.FinalizeTerminalTaskSideEffects(r.Context(), event)
	}
	h.persistChatRuntimeTokenStats(r.Context(), event.ChatSessionID, req.RuntimeStats)
	if workCompletion != nil && workCompletion.CompletedNow {
		h.emitIssueExecutedOnFirstCompletion(r, &workCompletion.Task)
	}
	for _, wake := range collaborationWakes {
		h.recordChannelAgentPromptWake(r.Context(), wake.channel, wake.agent, wake.trigger, wake.reason, wake.result)
	}
	h.TaskService.RecordEvolutionSkillOutcome(r.Context(), event.ID, "success", "success")
	h.TaskService.RecordEvolutionUnitUsed(r.Context(), event.ID)
	if chatDonePayload != nil {
		h.publishAgentInboxChatDone(event, *chatDonePayload)
	}
	// An explicit no-reply (including a source-completion abandoned draft),
	// onboarding sent/skipped/expired, and a completed transport reply are
	// separate observable outcomes. None is a delivery failure.
	lifecycleStatus := "completed"
	if terminalOutcome == "held" || terminalOutcome == "no_reply" || isChannelOnboarding {
		lifecycleStatus = terminalOutcome
	}
	h.publishAgentInboxTaskLifecycle(protocol.EventTaskCompleted, event, task.RuntimeID, lifecycleStatus)
	if terminalOutcome == "completed" {
		h.refreshAgentHonor(r.Context(), event.WorkspaceID, event.AgentID, "task_completed")
	}
	// T022: a terminal sandboxed diagnosis task maps onto its diagnosis run
	// (no-op for non-diagnosis tasks).
	h.mapDiagnosisInboxCompletion(r.Context(), event)
	// Non-diagnosis shared-dispatch business tasks may now have crossed the
	// all-terminal barrier; this is a no-op until every business task is done.
	go h.maybeStartSharedDispatchDiagnosis(context.Background(), event)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"acked_seq":        ackedSeq,
		"terminal_outcome": terminalOutcome,
		"resume_unsafe":    false,
	})
}

func persistAgentInboxCompletionTx(
	ctx context.Context,
	tx pgx.Tx,
	eventID pgtype.UUID,
	result []byte,
	sessionID, workDir string,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE agent_inbox_event
		SET result = $2,
		    session_id = NULLIF($3, ''),
		    work_dir = NULLIF($4, ''),
		    completed_at = COALESCE(completed_at, now()),
		    updated_at = now()
		WHERE id = $1`,
		eventID, result, sessionID, workDir)
	return err
}

func setAgentInboxCompletionFinalizationTx(
	ctx context.Context,
	qtx *db.Queries,
	tx pgx.Tx,
	event db.AgentInboxEvent,
	deliveryID, executionID pgtype.UUID,
	result []byte,
	terminalOutcome string,
) error {
	if _, err := qtx.SetAgentInboxTerminalOutcome(ctx, db.SetAgentInboxTerminalOutcomeParams{
		ID:                 event.ID,
		WorkspaceID:        event.WorkspaceID,
		TerminalOutcome:    strToText(terminalOutcome),
		TerminalDeliveryID: deliveryID,
		Retryable:          false,
	}); err != nil {
		return fmt.Errorf("record inbox terminal outcome: %w", err)
	}
	if !executionID.Valid {
		return nil
	}
	tag, err := tx.Exec(ctx, `
		UPDATE agent_execution
		SET status = 'completed',
		    result = $2,
		    completed_at = now()
		WHERE id = $1
		  AND source_event_id = $3
		  AND status = 'running'`,
		executionID, result, event.ID)
	if err != nil {
		return fmt.Errorf("complete agent execution: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func agentInboxCompletionTerminalOutcome(ctx context.Context, h *Handler, event db.AgentInboxEvent, req TaskCompleteRequest, payload *protocol.ChatDonePayload) string {
	_ = ctx
	_ = h
	_ = event
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
	// A lifecycle restart/reset has already invalidated this provider session.
	// The interrupted turn may report after that reset; never let its stale
	// token become the next resume source. Keep workDir because session reset
	// preserves the Agent Workspace and task working directories.
	if reasonCode == "restarted_by_user" {
		req.SessionID = ""
	}
	if failureReason != "" && event.Reason != channelOnboardingReason {
		if !event.ChatSessionID.Valid {
			h.completeFailedNonChatAgentInboxEvent(
				w, r, event, deliveryID, leaseToken, errText, failureReason, reasonCode, req.SessionID, req.WorkDir,
			)
			return
		}
		h.completeFailedAgentInboxEvent(
			w, r, event, deliveryID, leaseToken, errText, failureReason, reasonCode, req.SessionID, req.WorkDir, nil, false, 0,
		)
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
		slog.Warn("agent inbox fail: failed to reload delivery", "delivery_id", uuidToString(deliveryID), "error", err)
	}
	h.TaskService.RecordEvolutionSkillOutcome(ctx, event.ID, "failure", "failure")
	h.refreshAgentHonor(ctx, event.WorkspaceID, event.AgentID, "task_failed")
	if failureReason == "" {
		failureReason = string(taskfailure.Classify(errText))
	}
	h.applyAgentProviderQuotaBlock(ctx, event.WorkspaceID, event.AgentID, delivery.RuntimeID, event.ID, errText, failureReason)
}

func (h *Handler) completeFailedNonChatAgentInboxEvent(
	w http.ResponseWriter,
	r *http.Request,
	event db.AgentInboxEvent,
	deliveryID, leaseToken pgtype.UUID,
	errText, failureReason, reasonCode, sessionID, workDir string,
) {
	alreadyReplied := false
	terminalOutcome := "failed"
	retryable := inboxFailureRetryable(errText, failureReason, alreadyReplied)
	if alreadyReplied {
		terminalOutcome = "replied"
	}
	var collaborationWakes []channelAgentWake
	outcome, err := h.TaskService.FailAgentInboxTaskWithFinalization(
		r.Context(),
		service.AgentInboxDeliveryFence{
			DeliveryID:   deliveryID,
			InboxEventID: event.ID,
			LeaseToken:   leaseToken,
		},
		errText,
		sessionID,
		workDir,
		failureReason,
		func(qtx *db.Queries, tx pgx.Tx, outcome *service.FailTaskOutcome) error {
			event = outcome.Task
			if _, err := tx.Exec(r.Context(), `
				UPDATE agent_inbox_event
				SET last_error = $2,
				    updated_at = now()
				WHERE id = $1`, event.ID, errText); err != nil {
				return fmt.Errorf("record inbox failure: %w", err)
			}
			if _, err := qtx.SetAgentInboxTerminalOutcome(r.Context(), db.SetAgentInboxTerminalOutcomeParams{
				ID:                 event.ID,
				WorkspaceID:        event.WorkspaceID,
				TerminalOutcome:    strToText(terminalOutcome),
				TerminalDeliveryID: deliveryID,
				Retryable:          retryable,
			}); err != nil {
				return fmt.Errorf("record inbox failure outcome: %w", err)
			}
			var wakeErr error
			collaborationWakes, wakeErr = h.failCollaborationTurnTx(r.Context(), tx, event, errText)
			if wakeErr != nil {
				return fmt.Errorf("record collaboration turn failure: %w", wakeErr)
			}
			if _, err := tx.Exec(r.Context(), `
				UPDATE agent_execution
				SET status = 'failed',
				    error = $2,
				    failure_reason = NULLIF($3, ''),
				    completed_at = now()
				WHERE source_event_id = $1
				  AND status = 'running'`,
				event.ID, errText, failureReason); err != nil {
				return fmt.Errorf("fail agent execution: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "delivery lease is no longer active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to finalize inbox failure")
		return
	}
	event = outcome.Task
	if event.ChannelID.Valid {
		h.publishChannelTypingStopForInboxEvent(r.Context(), event)
	}
	h.finishFailedAgentInboxEvent(
		w, r, event, deliveryID, errText, failureReason, reasonCode, alreadyReplied, collaborationWakes, outcome.AckedSeq,
	)
}

func (h *Handler) completeFailedAgentInboxEvent(w http.ResponseWriter, r *http.Request, event db.AgentInboxEvent, deliveryID, leaseToken pgtype.UUID, errText, failureReason, reasonCode, sessionID, workDir string, completionResult []byte, deliveryAlreadyAcked bool, ackedSeq int64) {
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

	if !deliveryAlreadyAcked {
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
		ackedSeq = acked.SeqTo
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE agent_inbox_event
		SET result = CASE WHEN $4::jsonb IS NULL THEN result ELSE $4::jsonb END,
		    last_error = $2,
		    failure_reason = NULLIF($3, ''),
		    updated_at = now()
		WHERE id = $1`, event.ID, errText, failureReason, completionResult); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record inbox failure")
		return
	}
	alreadyReplied := false
	terminalOutcome := "failed"
	retryable := inboxFailureRetryable(errText, failureReason, alreadyReplied)
	if alreadyReplied {
		terminalOutcome = "replied"
	}
	if _, err := qtx.SetAgentInboxTerminalOutcome(r.Context(), db.SetAgentInboxTerminalOutcomeParams{
		ID:                 event.ID,
		WorkspaceID:        event.WorkspaceID,
		TerminalOutcome:    strToText(terminalOutcome),
		TerminalDeliveryID: deliveryID,
		Retryable:          retryable,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record inbox failure outcome")
		return
	}
	collaborationWakes, err := h.failCollaborationTurnTx(r.Context(), tx, event, errText)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record collaboration turn failure")
		return
	}

	if event.ChatSessionID.Valid {
		if reasonCode == "restarted_by_user" {
			if _, err := tx.Exec(r.Context(), `
				UPDATE chat_session
				SET session_id = NULL,
				    updated_at = now()
				WHERE id = $1`, event.ChatSessionID); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to clear restarted chat session")
				return
			}
		} else if chatFailureResumeUnsafe(failureReason) {
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
	if _, err := tx.Exec(r.Context(), `
		UPDATE agent_execution
		SET status = 'failed',
		    error = $2,
		    failure_reason = NULLIF($3, ''),
		    completed_at = now()
		WHERE source_event_id = $1
		  AND status = 'running'`,
		event.ID, errText, failureReason); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to finalize inbox execution ledger")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit inbox failure")
		return
	}
	if event.ChatSessionID.Valid {
		// Same gap as the completion path: a chat-session failure is acked here
		// and never reaches failTask. A failed rollout still has to close its
		// segment (an unclosed one makes the whole assembled DAG unusable) and
		// release its sandbox. Work-task failures route via failTask instead, so
		// they are excluded to avoid closing twice.
		h.TaskService.FinalizeTerminalTaskSideEffects(r.Context(), event)
	}
	h.finishFailedAgentInboxEvent(
		w, r, event, deliveryID, errText, failureReason, reasonCode, alreadyReplied, collaborationWakes, ackedSeq,
	)
}

func (h *Handler) finishFailedAgentInboxEvent(
	w http.ResponseWriter,
	r *http.Request,
	event db.AgentInboxEvent,
	deliveryID pgtype.UUID,
	errText, failureReason, reasonCode string,
	alreadyReplied bool,
	collaborationWakes []channelAgentWake,
	ackedSeq int64,
) {
	for _, wake := range collaborationWakes {
		h.recordChannelAgentPromptWake(r.Context(), wake.channel, wake.agent, wake.trigger, wake.reason, wake.result)
	}
	runtimeID := h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID)
	if alreadyReplied {
		h.TaskService.RecordEvolutionSkillOutcome(r.Context(), event.ID, "success", "success")
		h.TaskService.RecordEvolutionUnitUsed(r.Context(), event.ID)
		h.publishAgentInboxTaskLifecycle(protocol.EventTaskCompleted, event, runtimeID, "completed")
		// T022: a replied diagnosis task is a completion-equivalent terminal.
		h.mapDiagnosisInboxCompletion(r.Context(), event)
		go h.maybeStartSharedDispatchDiagnosis(context.Background(), event)
	} else {
		h.publishAgentInboxTaskLifecycle(protocol.EventTaskFailed, event, runtimeID, "failed")
		h.recordAgentInboxFailureActivity(r.Context(), event, deliveryID, errText, failureReason, reasonCode)
		// T022: a terminal sandboxed diagnosis task failure maps onto its run
		// with a classified cause (no-op for non-diagnosis tasks).
		h.mapDiagnosisInboxFailure(r.Context(), event, errText, failureReason, reasonCode)
		go h.maybeStartSharedDispatchDiagnosis(context.Background(), event)
	}
	terminalOutcome := "failed"
	if alreadyReplied {
		terminalOutcome = "replied"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"acked_seq":        ackedSeq,
		"terminal_outcome": terminalOutcome,
		"resume_unsafe":    !alreadyReplied && chatFailureResumeUnsafe(failureReason),
	})
}

var errAgentInboxEventNotCancellable = errors.New("agent inbox event is not cancellable")

// cancelledAgentInboxEventRow is the DB result of suppressing a wake event.
type cancelledAgentInboxEventRow struct {
	ID            pgtype.UUID
	AgentID       pgtype.UUID
	RuntimeID     pgtype.UUID
	Priority      int32
	CreatedAt     pgtype.Timestamptz
	TerminalAt    pgtype.Timestamptz
	ChatSessionID pgtype.UUID
	ChannelID     pgtype.UUID
}

type ChannelCancelAgentInboxEventResponse struct {
	OK           bool   `json:"ok"`
	InboxEventID string `json:"inbox_event_id"`
	AgentID      string `json:"agent_id"`
	Status       string `json:"status"`
}

func (h *Handler) cancelAgentInboxEventCore(ctx context.Context, workspaceUUID, inboxEventID pgtype.UUID) (cancelledAgentInboxEventRow, error) {
	var row cancelledAgentInboxEventRow
	err := h.DB.QueryRow(ctx, `
		WITH latest_delivery AS (
			SELECT d.id, d.runtime_id
			FROM agent_event_delivery d
			WHERE d.inbox_event_id = $1
			ORDER BY d.created_at DESC, d.id DESC
			LIMIT 1
		),
		cancelled_delivery AS (
			UPDATE agent_event_delivery d
			SET status = 'failed',
			    last_error = 'cancelled by user',
			    updated_at = now()
			WHERE d.inbox_event_id = $1
			  AND d.status IN ('leased', 'processing')
			RETURNING d.id, d.runtime_id
		),
		chosen_delivery AS (
			SELECT id, runtime_id FROM cancelled_delivery
			UNION ALL
			SELECT id, runtime_id FROM latest_delivery
			LIMIT 1
		),
		cancelled_event AS (
			UPDATE agent_inbox_event e
			SET status = 'suppressed',
			    terminal_outcome = 'no_reply',
			    terminal_delivery_id = (SELECT id FROM chosen_delivery LIMIT 1),
			    retryable = false,
			    terminal_at = now(),
			    acked_at = now(),
			    last_error = 'cancelled by user',
			    updated_at = now()
			WHERE e.id = $1
			  AND e.workspace_id = $2
			  AND e.requires_wake
			  AND e.status IN ('pending', 'draining', 'failed')
			  AND e.terminal_outcome IS NULL
			RETURNING e.id, e.agent_id, e.agent_session_id, e.priority, e.created_at, e.terminal_at, e.chat_session_id, e.channel_id
		)
		SELECT e.id,
		       e.agent_id,
		       COALESCE((SELECT runtime_id FROM chosen_delivery LIMIT 1), s.runtime_id),
		       e.priority,
		       e.created_at,
		       e.terminal_at,
		       e.chat_session_id,
		       e.channel_id
		FROM cancelled_event e
		LEFT JOIN agent_session s ON s.id = e.agent_session_id`, inboxEventID, workspaceUUID).Scan(
		&row.ID, &row.AgentID, &row.RuntimeID, &row.Priority, &row.CreatedAt, &row.TerminalAt, &row.ChatSessionID, &row.ChannelID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cancelledAgentInboxEventRow{}, errAgentInboxEventNotCancellable
		}
		return cancelledAgentInboxEventRow{}, err
	}
	return row, nil
}

func (h *Handler) cancelledInboxEventTaskResponse(row cancelledAgentInboxEventRow, workspaceID string) AgentTaskResponse {
	resp := AgentTaskResponse{
		ID:            uuidToString(row.ID),
		AgentID:       uuidToString(row.AgentID),
		RuntimeID:     uuidToString(row.RuntimeID),
		WorkspaceID:   workspaceID,
		Status:        "cancelled",
		Priority:      row.Priority,
		CompletedAt:   timestampToPtr(row.TerminalAt),
		Attempt:       1,
		MaxAttempts:   1,
		CreatedAt:     timestampToString(row.CreatedAt),
		ChatSessionID: uuidToString(row.ChatSessionID),
		Kind:          "chat",
	}
	if row.ChannelID.Valid {
		resp.ChannelID = uuidToString(row.ChannelID)
	}
	return resp
}

func (h *Handler) publishCancelledAgentInboxEvent(workspaceID, actorType, actorID string, row cancelledAgentInboxEventRow, payload AgentTaskResponse) {
	h.publishTask(protocol.EventTaskCancelled, workspaceID, actorType, actorID, uuidToString(row.ID), payload)
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
		DeliveryMode:    "execute",
		ResponseMode:    "public_response",
		RequiresWake:    event.RequiresWake,
		Priority:        event.Priority,
		SeqFrom:         event.SeqFrom,
		SeqTo:           event.SeqTo,
		LeaseToken:      uuidToString(delivery.LeaseToken),
		LeaseExpiresAt:  timestampToString(delivery.LeaseExpiresAt),
	}
	_ = h.DB.QueryRow(ctx, `SELECT delivery_mode, response_mode FROM agent_inbox_event WHERE id = $1`, event.ID).Scan(&resp.DeliveryMode, &resp.ResponseMode)
	if config, ok := service.TaskExecutionConfigFromContext(event.ExecutionConfig); ok {
		resp.ExecutionProfile = config.ExecutionProfile
	}
	if event.RequiresWake {
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
	if event.ChannelID.Valid {
		resp.ChannelID = uuidToString(event.ChannelID)
		var channelKind string
		if err := h.DB.QueryRow(ctx, `SELECT kind FROM channel WHERE id = $1`, event.ChannelID).Scan(&channelKind); err == nil {
			resp.ChannelKind = strings.TrimSpace(channelKind)
		}
	}
	resp.InboxEvent = &AgentInboxLeaseResponse{
		ID:             uuidToString(event.ID),
		DeliveryID:     uuidToString(delivery.ID),
		LeaseToken:     uuidToString(delivery.LeaseToken),
		LeaseExpiresAt: timestampToString(delivery.LeaseExpiresAt),
		SeqTo:          event.SeqTo,
		RequiresWake:   event.RequiresWake,
	}
	if _, ok := channelWakePromptFromContext(event.Context); ok {
		// LRM-1079: ordinary channel-only wakes carry the exact prompt in context.
		if !h.populateAgentInboxChannelWakeContext(ctx, event, &resp) {
			slog.Error("agent inbox claim: channel wake context missing",
				"inbox_event_id", uuidToString(event.ID),
				"reason", event.Reason,
				"channel_id", uuidToString(event.ChannelID),
			)
			return nil
		}
	} else if event.ChatSessionID.Valid {
		if !h.populateAgentInboxChatContext(ctx, event, &resp) {
			slog.Warn("agent inbox claim: exact prompt missing",
				"inbox_event_id", uuidToString(event.ID),
				"chat_session_id", uuidToString(event.ChatSessionID),
			)
			return nil
		}
	} else if channelOnlyWakeReason(event.Reason) {
		// Channel-only wake kinds must carry channel_wake context — do not fall
		// through to work-context claim (that would silently run without prompt).
		slog.Error("agent inbox claim: channel-only wake missing channel_wake context",
			"inbox_event_id", uuidToString(event.ID),
			"reason", event.Reason,
			"channel_id", uuidToString(event.ChannelID),
		)
		return nil
	} else if !h.populateAgentInboxWorkContext(ctx, runtime, event, &resp) {
		slog.Error("agent inbox claim: work context missing",
			"inbox_event_id", uuidToString(event.ID),
			"reason", event.Reason,
		)
		return nil
	}
	if agent, err := h.Queries.GetAgent(ctx, event.AgentID); err == nil {
		skills := h.TaskService.LoadAgentSkillsForInbox(ctx, event.AgentID, event.ID)
		skills = append(skills, h.builtinSkillsForAgent(ctx, agent)...)
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
		model := agent.Model.String
		thinkingLevel := agent.ThinkingLevel.String
		if config, ok := service.TaskExecutionConfigFromContext(event.ExecutionConfig); ok {
			model = config.Model
			thinkingLevel = config.ThinkingLevel
		}
		managerChannels := h.agentManagerChannels(ctx, event.WorkspaceID, agent.ID)
		// Reminder fires are single-channel: listing every managed channel
		// confuses the agent into posting the same patrol to all groups
		// (Frank 2026-08-03: 3 reminders × 3 channels = 3× spam).
		if strings.TrimSpace(event.Reason) == "reminder" && event.ChannelID.Valid {
			managerChannels = filterManagerChannelsTo(managerChannels, uuidToString(event.ChannelID))
		}
		resp.Agent = &TaskAgentData{
			ID:              uuidToString(agent.ID),
			Name:            agentDisplayName(agent),
			ManagedRole:     agent.ManagedRole.String,
			ManagerChannels: managerChannels,
			Instructions:    agent.Instructions,
			Skills:          skills,
			CustomEnv:       customEnv,
			CustomArgs:      customArgs,
			McpConfig:       mcpConfig,
			Model:           model,
			ThinkingLevel:   thinkingLevel,
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
	if resp.Agent != nil {
		resp.Agent.Memories = h.TaskService.LoadAgentMemoriesForExecution(ctx, event.AgentID, event.WorkspaceID, service.MemoryExecutionScope{
			InitiatorType: resp.InitiatorType,
			InitiatorID:   resp.InitiatorID,
			ProjectID:     resp.ProjectID,
			ChannelID:     resp.ChannelID,
			ChannelKind:   resp.ChannelKind,
			ChatSessionID: resp.ChatSessionID,
			IssueID:       resp.IssueID,
			MessageTexts:  []string{resp.ChatMessage, resp.TriggerCommentContent, resp.QuickCreatePrompt},
			TaskType:      resp.Kind,
			Now:           time.Now(),
		})
		// LRM-984: claim-time retrieval proof (injected) for each delivered memory.
		h.TaskService.RecordMemoryInjections(ctx, event.WorkspaceID, event.AgentID, event.ID, resp.Agent.Memories)
	}
	if strings.TrimSpace(resp.ChannelID) != "" {
		channelID := parseUUID(resp.ChannelID)
		if goal, err := h.currentChannelGoal(ctx, event.WorkspaceID, channelID); err == nil {
			h.hydrateChannelGoalWorkGraph(ctx, &goal)
			resp.ChannelGoal = channelGoalContextForClaim(goal)
			// LRM-1004: attach bounded subgoals for this claiming agent only.
			if resp.ChannelGoal != nil {
				resp.ChannelGoal.Subgoals = h.channelSubgoalContextsForClaim(ctx, event.WorkspaceID, channelID, event.AgentID, resp.ChannelGoal.ID)
			}
		}
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
	// D6-1a: put canonical runtime-state generation on every wake claim so the
	// daemon can later fence agentRuntimeTurnCoordinator.Begin. Soft-fail keeps
	// legacy resume (PriorSessionID from chat_session/issue) working if Ensure
	// cannot create/return a row (wrong binding, migration lag, etc.).
	// Do NOT surface FreshSessionNoticeReason here: migration-218 cutover rows
	// still pair with legacy PriorSession resume, and current daemons inject a
	// "brand new / history archived" brief whenever the notice is non-empty.
	// That false execution hint is reserved for D6-2 after archive/read-switch.
	h.attachCanonicalRuntimeState(ctx, event.AgentID, resp.RuntimeID, &resp)
	return &resp
}

func (h *Handler) agentManagerChannels(
	ctx context.Context,
	workspaceID, executionAgentID pgtype.UUID,
) []ManagerChannelData {
	rows, err := h.DB.Query(ctx, `
		WITH roster_agent AS (
		  SELECT COALESCE(source_agent_id, id) AS id
		  FROM agent
		  WHERE id = $1 AND workspace_id = $2
		)
		SELECT channel.id::text, channel.name
		FROM roster_agent
		JOIN channel_member member
		  ON member.workspace_id = $2
		 AND member.member_type = 'agent'
		 AND member.member_id = roster_agent.id
		 AND member.role = 'manager'
		JOIN channel
		  ON channel.workspace_id = member.workspace_id
		 AND channel.id = member.channel_id
		WHERE channel.archived_at IS NULL
		ORDER BY channel.name, channel.id`,
		executionAgentID,
		workspaceID,
	)
	if err != nil {
		slog.Warn("agent inbox claim: failed to load manager channels",
			"agent_id", uuidToString(executionAgentID),
			"workspace_id", uuidToString(workspaceID),
			"error", err,
		)
		return nil
	}
	defer rows.Close()

	var channels []ManagerChannelData
	for rows.Next() {
		var channel ManagerChannelData
		if err := rows.Scan(&channel.ID, &channel.Name); err != nil {
			slog.Warn("agent inbox claim: failed to scan manager channel",
				"agent_id", uuidToString(executionAgentID),
				"error", err,
			)
			return nil
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("agent inbox claim: failed to iterate manager channels",
			"agent_id", uuidToString(executionAgentID),
			"error", err,
		)
		return nil
	}
	return channels
}

// filterManagerChannelsTo keeps only the channel matching id (reminder fires).
func filterManagerChannelsTo(channels []ManagerChannelData, channelID string) []ManagerChannelData {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || len(channels) == 0 {
		return channels
	}
	out := make([]ManagerChannelData, 0, 1)
	for _, ch := range channels {
		if ch.ID == channelID {
			out = append(out, ch)
		}
	}
	return out
}

// attachCanonicalRuntimeState ensures the agent×runtime row exists and copies
// generation onto the claim response. It intentionally omits
// FreshSessionNoticeReason until D6-2 completes archive + resume cutover, and
// never mutates PriorSessionID/PriorWorkDir.
func (h *Handler) attachCanonicalRuntimeState(ctx context.Context, agentID pgtype.UUID, runtimeID string, resp *AgentTaskResponse) {
	if h == nil || resp == nil || !agentID.Valid || strings.TrimSpace(runtimeID) == "" {
		return
	}
	runtimeUUID := parseUUID(runtimeID)
	if !runtimeUUID.Valid {
		return
	}
	state, err := h.Queries.EnsureAgentRuntimeState(ctx, db.EnsureAgentRuntimeStateParams{
		AgentID:   agentID,
		RuntimeID: runtimeUUID,
	})
	if err != nil {
		slog.Warn("agent inbox claim: ensure agent_runtime_state failed",
			"agent_id", uuidToString(agentID),
			"runtime_id", runtimeID,
			"error", err,
		)
		return
	}
	if state.Generation > 0 {
		resp.RuntimeStateGeneration = state.Generation
	}
	// Intentionally do not copy state.FreshSessionNoticeReason.
}

func agentInboxSyntheticTask(event db.AgentInboxEvent, runtimeID pgtype.UUID) db.AgentInboxEvent {
	if event.RuntimeID.Valid {
		runtimeID = event.RuntimeID
	}
	return db.AgentInboxEvent{
		ID:                event.ID,
		WorkspaceID:       event.WorkspaceID,
		AgentSessionID:    event.AgentSessionID,
		ConversationID:    event.ConversationID,
		ChannelID:         event.ChannelID,
		AgentID:           event.AgentID,
		RuntimeID:         runtimeID,
		ExecutionConfig:   event.ExecutionConfig,
		Context:           event.Context,
		IssueID:           event.IssueID,
		ChatSessionID:     event.ChatSessionID,
		AutopilotRunID:    event.AutopilotRunID,
		TriggerCommentID:  event.TriggerCommentID,
		TriggerSummary:    event.TriggerSummary,
		InitiatorUserID:   event.InitiatorUserID,
		ForceFreshSession: event.ForceFreshSession,
		IsLeaderTask:      event.IsLeaderTask,
		SessionID:         event.SessionID,
		WorkDir:           event.WorkDir,
		Status:            "running",
		Priority:          event.Priority,
		Attempt:           event.Attempt,
		MaxAttempts:       event.MaxAttempts,
		ParentTaskID:      event.ParentTaskID,
		AgentDmExchangeID: event.AgentDmExchangeID,
		AgentDmTurn:       event.AgentDmTurn,
		CreatedAt:         event.CreatedAt,
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
		SELECT COALESCE(latest_delivery.runtime_id, e.runtime_id, s.runtime_id, a.runtime_id)
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

func agentInboxTaskMessagePayload(event db.AgentInboxEvent, msg TaskMessageRequest, kind string, details map[string]any) (protocol.TaskMessagePayload, bool) {
	payload := protocol.TaskMessagePayload{
		TaskID:     uuidToString(event.ID),
		Seq:        msg.Seq,
		Visibility: "user_facing",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	// Thinking is retained as diagnostic activity only and is never forwarded
	// to a user client, regardless of whether it carries provider text.
	if msg.Type == "thinking" {
		return protocol.TaskMessagePayload{}, false
	}
	switch kind {
	case "tool_call":
		payload.Type = "tool_use"
		payload.Tool = stringFromMap(details, "tool")
		if payload.Tool == "" {
			return protocol.TaskMessagePayload{}, false
		}
		payload.Input = msg.Input
	case "error":
		payload.Type = "error"
		payload.Content = agentInboxActivityMessageText(msg)
	default:
		return protocol.TaskMessagePayload{}, false
	}
	return payload, true
}

// parseProviderLineageSubagentType reads the Claude nativeLineage JSON shape
// {"parent_tool_use_id":"...","subagent_type":"Explore"}. Empty lineage or
// non-JSON returns ok=false. A non-empty parent without subagent_type still
// counts as a nested trajectory (ok=true, type empty).
func parseProviderLineageSubagentType(lineage string) (subagentType string, ok bool) {
	lineage = strings.TrimSpace(lineage)
	if lineage == "" {
		return "", false
	}
	var payload struct {
		ParentToolUseID string `json:"parent_tool_use_id"`
		SubagentType    string `json:"subagent_type"`
	}
	if err := json.Unmarshal([]byte(lineage), &payload); err != nil {
		// Non-JSON lineage still means a nested trajectory was stamped.
		return "", true
	}
	if payload.ParentToolUseID == "" && payload.SubagentType == "" {
		return "", false
	}
	return strings.TrimSpace(payload.SubagentType), true
}

func providerSubagentStartedMessage(subagentType string) string {
	subagentType = strings.TrimSpace(subagentType)
	if subagentType == "" {
		return "Subagent started"
	}
	return "Subagent started: " + subagentType
}

// isCursorLikeSubagentTool mirrors packages/views/chat/lib/bubble-cursor-activity
// classifyBubbleToolKind === "task": Task / subagent / best-of-n / launch-agent
// tools, or any tool whose input carries subagent_type.
func isCursorLikeSubagentTool(tool string, input map[string]any) bool {
	if input != nil {
		if st, ok := input["subagent_type"].(string); ok && strings.TrimSpace(st) != "" {
			return true
		}
	}
	n := normalizeActivityToolSlug(tool)
	if n == "" {
		return false
	}
	if n == "task" || strings.HasPrefix(n, "task") {
		return true
	}
	if strings.Contains(n, "subagent") || strings.Contains(n, "bestofn") || strings.Contains(n, "launchagent") {
		return true
	}
	return false
}

func normalizeActivityToolSlug(tool string) string {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if tool == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(tool))
	for _, r := range tool {
		switch r {
		case '-', '_', ' ', '	':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func cursorSubagentStartedMessage(tool string, input map[string]any) string {
	title := ""
	if input != nil {
		for _, key := range []string{"description", "name", "subagent_type"} {
			if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
				title = strings.TrimSpace(value)
				break
			}
		}
	}
	if title == "" {
		title = strings.TrimSpace(tool)
	}
	if title == "" {
		return "Subagent started"
	}
	return "Subagent started: " + title
}

func agentInboxActivityMessageKind(messageType string) (kind, eventType, severity string) {
	switch messageType {
	case "thinking":
		// Provider reasoning remains diagnostic-only. Empty thinking is handled
		// earlier as a legacy phase row, but current daemons do not send it.
		return "thinking", "runtime_thinking", "info"
	case "tool_use":
		return "tool_call", "tool_use", "info"
	case "tool_result":
		return "tool_output", "tool_result", "info"
	case "error":
		return "error", "error", "error"
	case "log":
		return "custom", "runtime_text", "info"
	case "text":
		// For inbox/direct runs, streaming text is not necessarily a user-visible
		// reply: several runtimes emit wrapper logs or plan narration on this
		// channel. Visible replies get their own message_sent event on completion
		// or transport send, so keep raw runtime text diagnostic-only.
		return "custom", "runtime_text", "info"
	default:
		return "custom", "runtime_message", "info"
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

func (h *Handler) populateAgentInboxWorkContext(ctx context.Context, runtime db.AgentRuntime, event db.AgentInboxEvent, resp *AgentTaskResponse) bool {
	resp.WorkspaceID = uuidToString(event.WorkspaceID)
	if event.InitiatorUserID.Valid {
		resp.InitiatorType = "member"
		resp.InitiatorID = uuidToString(event.InitiatorUserID)
		if user, err := h.Queries.GetUser(ctx, event.InitiatorUserID); err == nil {
			resp.InitiatorName = userDisplayName(user)
			resp.InitiatorEmail = user.Email
		}
	}

	loadProject := func(projectID pgtype.UUID) {
		if !projectID.Valid {
			return
		}
		resp.ProjectID = uuidToString(projectID)
		if project, err := h.Queries.GetProject(ctx, projectID); err == nil {
			resp.ProjectTitle = project.Title
		}
	}

	if event.IssueID.Valid {
		issue, err := h.Queries.GetIssue(ctx, event.IssueID)
		if err != nil || issue.WorkspaceID != event.WorkspaceID {
			return false
		}
		resp.ThreadName = issue.Title
		if !event.TriggerCommentID.Valid {
			snapshot, found, err := service.IssueAssignmentSnapshotFromContext(event.Context)
			if err != nil {
				slog.Error("agent inbox claim: invalid assignment snapshot",
					"inbox_event_id", uuidToString(event.ID),
					"error", err,
				)
				return false
			}
			if found {
				snapshot.Status = issue.Status
				resp.AssignmentSnapshot = &snapshot
				resp.ThreadName = snapshot.Title
			}
		}
		loadProject(issue.ProjectID)
		if event.TriggerCommentID.Valid {
			if comment, err := h.Queries.GetComment(ctx, event.TriggerCommentID); err == nil {
				resp.TriggerCommentContent = comment.Content
				resp.TriggerThreadID = uuidToString(comment.ID)
				if comment.ParentID.Valid {
					resp.TriggerThreadID = uuidToString(comment.ParentID)
				}
				resp.TriggerAuthorType = comment.AuthorType
				resp.InitiatorType = comment.AuthorType
				resp.InitiatorID = uuidToString(comment.AuthorID)
				switch comment.AuthorType {
				case "agent":
					if author, err := h.Queries.GetAgent(ctx, comment.AuthorID); err == nil {
						resp.TriggerAuthorName = agentDisplayName(author)
						resp.InitiatorName = resp.TriggerAuthorName
					}
				case "member":
					if user, err := h.Queries.GetUser(ctx, comment.AuthorID); err == nil {
						resp.TriggerAuthorName = userDisplayName(user)
						resp.InitiatorName = resp.TriggerAuthorName
						resp.InitiatorEmail = user.Email
					}
				}
				if startedAt, err := h.Queries.GetLastTaskStartedAtForIssueAndAgent(ctx, db.GetLastTaskStartedAtForIssueAndAgentParams{
					AgentID: event.AgentID,
					IssueID: comment.IssueID,
				}); err == nil && startedAt.Valid {
					if count, err := h.Queries.CountNewCommentsSince(ctx, db.CountNewCommentsSinceParams{
						AnchorID:    event.TriggerCommentID,
						IssueID:     comment.IssueID,
						WorkspaceID: comment.WorkspaceID,
						Since:       startedAt,
						AuthorID:    event.AgentID,
					}); err == nil && count > 0 {
						resp.NewCommentCount = int(count)
						resp.NewCommentsSince = startedAt.Time.UTC().Format(time.RFC3339)
					}
				}
			}
		}
		if resp.TriggerCommentContent != "" {
			references := h.hydrateReferencedEntities(
				ctx,
				resp.WorkspaceID,
				resp.InitiatorType,
				resp.InitiatorID,
				referencedEntitySource{Content: resp.TriggerCommentContent},
			)
			resp.ReferencedEntities = references.Snapshots
			resp.ReferencedEntityOmittedCount = references.OmittedCount
		}
		if !event.ForceFreshSession {
			if prior, err := h.Queries.GetLastTaskSession(ctx, db.GetLastTaskSessionParams{
				AgentID: event.AgentID,
				IssueID: event.IssueID,
			}); err == nil {
				if prior.RuntimeID == runtime.ID && prior.SessionID.Valid {
					resp.PriorSessionID = prior.SessionID.String
				}
				if prior.WorkDir.Valid {
					resp.PriorWorkDir = prior.WorkDir.String
				}
			}
		}
		return true
	}

	// Autopilot runtime tables dropped (LRM-1051). Historical AutopilotRunID
	// cannot hydrate title/payload; fall through to other task kinds.
	if event.AutopilotRunID.Valid {
		return true
	}

	var quickCreate service.QuickCreateContext
	if json.Unmarshal(event.Context, &quickCreate) == nil && quickCreate.Type == service.QuickCreateContextType {
		resp.Kind = "quick_create"
		resp.QuickCreatePrompt = quickCreate.Prompt
		// Merge modal uploads + source-chat images into the CLI env so
		// `issue create` binds them even when the agent omits --attachment-id
		// (LRM-731). Source IDs remain listed separately in the prompt.
		resp.QuickCreateAttachmentIDs = append([]string(nil), quickCreate.AttachmentIDs...)
		resp.ThreadName = quickCreate.Prompt
		if quickCreate.Source != nil {
			source := *quickCreate.Source
			source.AttachmentIDs = append([]string(nil), quickCreate.Source.AttachmentIDs...)
			resp.QuickCreateSource = &source
			seen := make(map[string]struct{}, len(resp.QuickCreateAttachmentIDs)+len(source.AttachmentIDs))
			for _, id := range resp.QuickCreateAttachmentIDs {
				seen[id] = struct{}{}
			}
			for _, id := range source.AttachmentIDs {
				if id == "" {
					continue
				}
				if _, ok := seen[id]; ok {
					continue
				}
				seen[id] = struct{}{}
				resp.QuickCreateAttachmentIDs = append(resp.QuickCreateAttachmentIDs, id)
			}
		}
		if projectID, err := util.ParseUUID(quickCreate.ProjectID); err == nil {
			loadProject(projectID)
		}
		resp.ParentIssueID = quickCreate.ParentIssueID
		return strings.TrimSpace(resp.QuickCreatePrompt) != ""
	}

	// Environment dispatch, memory curation, Reminder, and other internal
	// wake kinds carry their exact prompt/config in the canonical context.
	resp.Kind = event.Reason
	resp.ThreadName = event.TriggerSummary.String
	return len(event.Context) > 0 || len(event.ExecutionConfig) > 0
}

func (h *Handler) populateAgentInboxChannelWakeContext(ctx context.Context, event db.AgentInboxEvent, resp *AgentTaskResponse) bool {
	prompt, ok := channelWakePromptFromContext(event.Context)
	if !ok {
		return false
	}
	resp.WorkspaceID = uuidToString(event.WorkspaceID)
	resp.ChannelID = uuidToString(event.ChannelID)
	resp.Kind = event.Reason
	resp.ChatMessage = prompt
	resp.ThreadName = event.TriggerSummary.String
	if strings.TrimSpace(resp.ThreadName) == "" {
		resp.ThreadName = prompt
	}
	if event.InitiatorUserID.Valid {
		resp.InitiatorType = "member"
		resp.InitiatorID = uuidToString(event.InitiatorUserID)
		if user, err := h.Queries.GetUser(ctx, event.InitiatorUserID); err == nil {
			resp.InitiatorName = userDisplayName(user)
			resp.InitiatorEmail = user.Email
		}
	} else if event.SourceMessageID.Valid {
		h.populateAgentInboxInitiator(ctx, event.SourceMessageID, resp)
	}
	if event.SourceMessageID.Valid {
		if atts, attErr := h.Queries.ListAttachmentsByChannelMessageIDs(ctx, db.ListAttachmentsByChannelMessageIDsParams{
			Column1:     []pgtype.UUID{event.SourceMessageID},
			WorkspaceID: event.WorkspaceID,
		}); attErr == nil {
			seen := make(map[string]struct{}, len(atts))
			for _, row := range atts {
				id := uuidToString(row.Attachment.ID)
				if _, exists := seen[id]; exists {
					continue
				}
				seen[id] = struct{}{}
				resp.ChatMessageAttachments = append(resp.ChatMessageAttachments, ChatAttachmentMeta{
					ID:          id,
					Filename:    row.Attachment.Filename,
					ContentType: row.Attachment.ContentType,
				})
			}
		}
	}
	var channelProjectID pgtype.UUID
	_ = h.DB.QueryRow(ctx, `SELECT project_id FROM channel WHERE id = $1`, event.ChannelID).Scan(&channelProjectID)
	if channelProjectID.Valid {
		resp.ProjectID = uuidToString(channelProjectID)
		if project, err := h.Queries.GetProject(ctx, channelProjectID); err == nil {
			resp.ProjectTitle = project.Title
		}
	}
	references := h.hydrateReferencedEntities(
		ctx,
		resp.WorkspaceID,
		resp.InitiatorType,
		resp.InitiatorID,
		referencedEntitySource{Content: resp.ChatMessage},
	)
	resp.ReferencedEntities = references.Snapshots
	resp.ReferencedEntityOmittedCount = references.OmittedCount
	return true
}

// channelOnlyWakeReason is true for ordinary channel wakes that must carry
// agent_inbox_event.context.channel_wake (no chat_session bridge).
func channelOnlyWakeReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "mention", "dm", "reminder", "thread_reply", "handoff", "continuation", channelMessageWakeReason:
		return true
	default:
		return false
	}
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
	if event.InitiatorUserID.Valid {
		resp.InitiatorType = "member"
		resp.InitiatorID = uuidToString(event.InitiatorUserID)
		if user, err := h.Queries.GetUser(ctx, event.InitiatorUserID); err == nil {
			resp.InitiatorName = userDisplayName(user)
			resp.InitiatorEmail = user.Email
		}
	}

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
	}
	runtimeMatches := func(runtimeID pgtype.UUID) bool {
		return runtimeID.Valid && resp.RuntimeID != "" && uuidToString(runtimeID) == resp.RuntimeID
	}
	if !event.ForceFreshSession {
		if runtimeMatches(cs.RuntimeID) && cs.WorkDir.Valid {
			resp.PriorWorkDir = cs.WorkDir.String
		}
		if runtimeMatches(cs.RuntimeID) && cs.SessionID.Valid {
			resp.PriorSessionID = cs.SessionID.String
		}
		if prior, err := h.Queries.GetLastChatTaskSession(ctx, cs.ID); err == nil {
			if runtimeMatches(prior.RuntimeID) && prior.WorkDir.Valid && resp.PriorWorkDir == "" {
				resp.PriorWorkDir = prior.WorkDir.String
			}
			if prior.SessionID.Valid && runtimeMatches(prior.RuntimeID) && resp.PriorSessionID == "" {
				resp.PriorSessionID = prior.SessionID.String
			}
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
			for _, row := range atts {
				appendAttachments([]db.Attachment{row.Attachment})
			}
		}
	}
	if msgs, err := h.Queries.ListChatMessages(ctx, cs.ID); err == nil && len(msgs) > 0 {
		promptMessages := inboxPromptMessages(msgs, event.ID)
		if len(promptMessages) == 0 {
			return false
		}
		parts := make([]string, 0, len(promptMessages))
		referenceSources := make([]referencedEntitySource, 0, len(promptMessages))
		for _, m := range promptMessages {
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, m.Content)
			}
			source := referencedEntitySource{Content: m.Content}
			if len(m.Parts) > 0 {
				_ = json.Unmarshal(m.Parts, &source.Parts)
			}
			referenceSources = append(referenceSources, source)
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
			channelID := resp.ChannelID
			if channelID == "" {
				var linkedChannelID pgtype.UUID
				err := h.DB.QueryRow(ctx, `
					SELECT channel_id
					FROM channel_agent_session
					WHERE chat_session_id = $1
				`, cs.ID).Scan(&linkedChannelID)
				switch {
				case err == nil:
					channelID = uuidToString(linkedChannelID)
				case !errors.Is(err, pgx.ErrNoRows):
					// An unknown session kind must not be treated as a direct
					// chat, because that could re-parse a synthetic channel
					// prompt and hydrate its member directory.
					return false
				}
			}
			if channelID == "" {
				references := h.hydrateReferencedEntities(
					ctx,
					resp.WorkspaceID,
					resp.InitiatorType,
					resp.InitiatorID,
					referenceSources...,
				)
				resp.ReferencedEntities = references.Snapshots
				resp.ReferencedEntityOmittedCount = references.OmittedCount
			}
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
			// Research fleet wakes use chat_session title research:<sessionUUID>.
			// Production completions go through this inbox path (not TaskService.
			// CompleteTask), so mirror here or the Research UI never sees replies.
			if h.TaskService != nil {
				h.TaskService.MirrorResearchChatReply(ctx, event, row)
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
