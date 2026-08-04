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
	delivery, err := h.leaseAgentInboxEventForRuntime(r.Context(), runtime)
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
	pending, _ := h.countReadyAgentInboxEventsForRuntime(r.Context(), runtime)
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
		writeError(w, http.StatusInternalServerError, "inbox event has invalid execution context")
		return
	}
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
		details := map[string]any{
			"inbox_event_id":    uuidToString(event.ID),
			"delivery_id":       uuidToString(deliveryID),
			"source_message_id": uuidToString(event.SourceMessageID),
			"seq":               msg.Seq,
		}
		if lineage := strings.TrimSpace(msg.Lineage); lineage != "" {
			details["lineage"] = lineage
			// P0: Claude stream-json (and any provider that stamps Message.Lineage)
			// surfaces nested trajectories. Emit one user-facing Activity row the
			// first time we see each lineage on this inbox event — FE already
			// treats event_type containing "subagent" as Subagent activity.
			h.maybeRecordProviderSubagentStarted(r.Context(), event, runtimeID, deliveryID, lineage, details)
		}
		if taskMessageIsPhaseStatus(msg.Type, msg.Content) {
			// Legacy daemons may still report an empty thinking phase. Retain it
			// as diagnostic data only; current daemons no longer emit this wire.
			details["phase_status"] = true
			insertAgentActivityEvent(r.Context(), h.DB,
				event.WorkspaceID, event.AgentID, runtimeID, pgtype.UUID{},
				activityKindThinking, "runtime_phase", "info",
				targetKind, targetID, "",
				"", "", details,
			)
			if payload, ok := agentInboxTaskMessagePayload(event, msg, activityKindThinking, details); ok && h.Bus != nil {
				h.publishTask(protocol.EventTaskMessage, uuidToString(event.WorkspaceID), "system", "", uuidToString(event.ID), payload)
			}
			continue
		}
		kind, eventType, severity := agentInboxActivityMessageKind(msg.Type)
		message := agentInboxActivityMessageText(msg)
		if callID := strings.TrimSpace(msg.CallID); callID != "" {
			details["call_id"] = callID
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
			// Cursor (and similar) launch nested agents as Task/subagent tools —
			// same classification as packages/views/chat/lib/bubble-cursor-activity.
			rawTool := strings.TrimSpace(msg.Tool)
			if isCursorLikeSubagentTool(rawTool, msg.Input) {
				h.maybeRecordCursorSubagentStarted(r.Context(), event, runtimeID, deliveryID, msg, details)
			}
		}
		// Cursor often emits tool args only on completed (tool_result). The
		// daemon already carries that Input (runtime_tool_event.go); merge it
		// into the started tool_call row by call_id instead of inventing a
		// second user-facing line. Existing facts are preserved.
		if msg.Type == "tool_result" {
			h.backfillAgentInboxToolCallFromResult(r.Context(), h.DB,
				event.WorkspaceID, event.AgentID, uuidToString(deliveryID), msg)
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
	var freshnessResolutionPublications []agentTransportFreshnessResolutionPublication
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
					freshnessResolutionPublications, err = h.abandonAgentTransportFreshnessDraftsWithExec(
						r.Context(), tx, event, task.RuntimeID,
					)
					return err
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
					if len(freshnessResolutionPublications) > 0 {
						terminalOutcome = "no_reply"
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
		// Channel-only completions never publish ChatDone, so settle ambient
		// pending wakes here (legacy path settled inside handleChannelChatDone).
		if event.ChannelID.Valid {
			h.settleChannelAmbientWakeForTask(r.Context(), event.ID, true)
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

		freshnessResolutionPublications, err = h.abandonAgentTransportFreshnessDraftsWithExec(
			r.Context(), tx, event, task.RuntimeID,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve inbox freshness draft")
			return
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
		if len(freshnessResolutionPublications) > 0 {
			terminalOutcome = "no_reply"
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
	h.publishAgentTransportFreshnessResolutions(r.Context(), freshnessResolutionPublications)
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
		h.recordAgentInboxVisibleOutputActivity(r.Context(), event, task.RuntimeID, *chatDonePayload)
	}
	h.recordAgentInboxStatusActivity(r.Context(), event, task.RuntimeID, deliveryID, agentInboxStatusActivityIdle)
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
	if h.inboxEventHasAgentTransportVisibleOutput(ctx, event.ID) {
		return "replied"
	}
	if h.inboxEventHasAgentTransportFreshnessHold(ctx, event.ID) {
		return "held"
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
	h.refreshAgentHonor(ctx, event.WorkspaceID, event.AgentID, "task_failed")
	if failureReason == "" {
		failureReason = string(taskfailure.Classify(errText))
	}
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
	h.applyAgentProviderQuotaBlock(ctx, event.WorkspaceID, event.AgentID, delivery.RuntimeID, event.ID, errText, failureReason)
}

func (h *Handler) completeFailedNonChatAgentInboxEvent(
	w http.ResponseWriter,
	r *http.Request,
	event db.AgentInboxEvent,
	deliveryID, leaseToken pgtype.UUID,
	errText, failureReason, reasonCode, sessionID, workDir string,
) {
	alreadyReplied := h.inboxEventHasAgentTransportVisibleOutput(r.Context(), event.ID)
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
		h.settleChannelAmbientWakeForTask(r.Context(), event.ID, false)
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
	alreadyReplied := h.inboxEventHasAgentTransportVisibleOutput(r.Context(), event.ID)
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
	} else {
		h.publishAgentInboxTaskLifecycle(protocol.EventTaskFailed, event, runtimeID, "failed")
		h.recordAgentInboxFailureActivity(r.Context(), event, deliveryID, errText, failureReason, reasonCode)
		// T022: a terminal sandboxed diagnosis task failure maps onto its run
		// with a classified cause (no-op for non-diagnosis tasks).
		h.mapDiagnosisInboxFailure(r.Context(), event, errText, failureReason, reasonCode)
	}
	h.recordAgentInboxStatusActivity(r.Context(), event, runtimeID, deliveryID, agentInboxStatusActivityIdle)
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

type ChannelCancelActiveAgentInboxResponse struct {
	OK             bool                                   `json:"ok"`
	CancelledCount int                                    `json:"cancelled_count"`
	Cancelled      []ChannelCancelAgentInboxEventResponse `json:"cancelled"`
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

func (h *Handler) listCancellableChannelActiveInboxEventIDs(ctx context.Context, workspaceUUID, channelID pgtype.UUID) ([]pgtype.UUID, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT e.id
		FROM agent_inbox_event e
		WHERE e.channel_id = $1
		  AND e.workspace_id = $2
		  AND e.requires_wake
		  AND e.status IN ('pending', 'draining', 'failed')
		  AND e.terminal_outcome IS NULL
		  AND e.reason NOT IN ('ambient', 'channel_onboarding')
		ORDER BY e.created_at ASC, e.id ASC`, channelID, workspaceUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]pgtype.UUID, 0)
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CancelChannelAgentInboxEvent stops one channel wake by inbox_event_id
// (LRM-425 single-stop contract). Authoritative id matches active-tasks.
func (h *Handler) CancelChannelAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
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

	var exists bool
	if err := h.DB.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1
			FROM agent_inbox_event e
			WHERE e.id = $1
			  AND e.workspace_id = $2
			  AND e.channel_id = $3
			  AND e.requires_wake
		)`, eventID, workspaceUUID, channelID).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve inbox event")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "inbox event not found")
		return
	}

	row, err := h.cancelAgentInboxEventCore(r.Context(), workspaceUUID, eventID)
	if err != nil {
		if errors.Is(err, errAgentInboxEventNotCancellable) {
			writeError(w, http.StatusConflict, "inbox event is not cancellable")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to cancel inbox event")
		return
	}

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	payload := h.cancelledInboxEventTaskResponse(row, workspaceID)
	h.publishCancelledAgentInboxEvent(workspaceID, actorType, actorID, row, payload)
	writeJSON(w, http.StatusOK, ChannelCancelAgentInboxEventResponse{
		OK:           true,
		InboxEventID: uuidToString(row.ID),
		AgentID:      uuidToString(row.AgentID),
		Status:       "cancelled",
	})
}

// CancelChannelActiveAgentInboxEvents stops every active (cancellable) wake in
// the channel in one request — Stop All must not loop single cancel (LRM-425).
func (h *Handler) CancelChannelActiveAgentInboxEvents(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}

	eventIDs, err := h.listCancellableChannelActiveInboxEventIDs(r.Context(), workspaceUUID, channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list active inbox events")
		return
	}

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	cancelled := make([]ChannelCancelAgentInboxEventResponse, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		row, err := h.cancelAgentInboxEventCore(r.Context(), workspaceUUID, eventID)
		if err != nil {
			if errors.Is(err, errAgentInboxEventNotCancellable) {
				// Race: event terminated between list and cancel — skip.
				continue
			}
			writeError(w, http.StatusInternalServerError, "failed to cancel active inbox events")
			return
		}
		payload := h.cancelledInboxEventTaskResponse(row, workspaceID)
		h.publishCancelledAgentInboxEvent(workspaceID, actorType, actorID, row, payload)
		cancelled = append(cancelled, ChannelCancelAgentInboxEventResponse{
			OK:           true,
			InboxEventID: uuidToString(row.ID),
			AgentID:      uuidToString(row.AgentID),
			Status:       "cancelled",
		})
	}

	writeJSON(w, http.StatusOK, ChannelCancelActiveAgentInboxResponse{
		OK:             true,
		CancelledCount: len(cancelled),
		Cancelled:      cancelled,
	})
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
	var reason string
	if err := h.DB.QueryRow(r.Context(), `
		SELECT reason
		FROM agent_inbox_event
		WHERE id = $1 AND workspace_id = $2 AND channel_id = $3`,
		eventID, parseUUID(workspaceID), channelID).Scan(&reason); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "inbox event is not retryable")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to inspect inbox event")
		return
	}
	if reason == channelOnboardingReason {
		writeError(w, http.StatusConflict, "channel onboarding retries reuse the canonical inbox event")
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
	if event.ChannelID.Valid && event.SeqTo > 0 {
		from := event.SeqFrom - 1
		if from < 0 {
			from = 0
		}
		resp.Messages = h.channelAmbientUnreadMessages(ctx, h.DB, uuidToString(event.WorkspaceID), uuidToString(event.ChannelID), from, event.SeqTo, agentInboxDrainMessageLimit)
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
			resp.ChannelGoal = channelGoalContextForClaim(goal)
			// LRM-1004: attach bounded subgoals for this claiming agent only.
			if resp.ChannelGoal != nil {
				resp.ChannelGoal.Subgoals = h.channelSubgoalContextsForClaim(ctx, event.WorkspaceID, channelID, event.AgentID, resp.ChannelGoal.ID)
			}
			// LRM-985: auditable Goal reinjection on every wake that carries a goal.
			if resp.ChannelGoal != nil {
				h.recordAgentActivityEvent(ctx, h.DB,
					event.WorkspaceID, event.AgentID, event.RuntimeID, event.ID,
					activityKindCustom, "channel_goal_injected", "info",
					"channel", channelID, "",
					"", "Channel goal reinjected for this wake",
					map[string]any{
						"goal_id":                     resp.ChannelGoal.ID,
						"goal_version":                resp.ChannelGoal.Version,
						"inbox_event_id":              uuidToString(event.ID),
						"channel_id":                  resp.ChannelID,
						"prior_session_id":            resp.PriorSessionID,
						"fresh_session_notice_reason": resp.FreshSessionNoticeReason,
						"trigger":                     "claim",
					},
				)
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
	// Thinking is retained as diagnostic activity only and is never forwarded
	// to a user client, regardless of whether it carries provider text.
	if msg.Type == "thinking" {
		return protocol.TaskMessagePayload{}, false
	}
	switch kind {
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

// maybeRecordProviderSubagentStarted inserts one custom Activity event per
// (inbox event, lineage) when a provider message carries Message.Lineage.
// Fail-soft: duplicate lookup / insert errors never fail the message report.
func (h *Handler) maybeRecordProviderSubagentStarted(
	ctx context.Context,
	event db.AgentInboxEvent,
	runtimeID, deliveryID pgtype.UUID,
	lineage string,
	baseDetails map[string]any,
) {
	if h == nil || h.DB == nil {
		return
	}
	subagentType, ok := parseProviderLineageSubagentType(lineage)
	if !ok {
		return
	}
	if h.providerSubagentStartedExists(ctx, event.WorkspaceID, event.AgentID, event.ID, lineage) {
		return
	}
	targetKind, targetID := agentInboxActivityTarget(event)
	details := map[string]any{
		"lineage":           lineage,
		"inbox_event_id":    uuidToString(event.ID),
		"source_message_id": uuidToString(event.SourceMessageID),
	}
	if deliveryID.Valid {
		details["delivery_id"] = uuidToString(deliveryID)
	}
	if subagentType != "" {
		details["subagent_type"] = subagentType
	}
	// Preserve seq when the caller already had one on the message details.
	if baseDetails != nil {
		if seq, exists := baseDetails["seq"]; exists {
			details["seq"] = seq
		}
	}
	h.recordAgentActivityEvent(ctx, h.DB,
		event.WorkspaceID, event.AgentID, runtimeID, pgtype.UUID{},
		activityKindCustom, "subagent_started", "info",
		targetKind, targetID, "",
		"provider_lineage", providerSubagentStartedMessage(subagentType),
		details,
	)
}

func (h *Handler) providerSubagentStartedExists(ctx context.Context, workspaceID, agentID, inboxEventID pgtype.UUID, lineage string) bool {
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
    AND COALESCE(details->>'lineage', '') = $6
)
`, workspaceID, agentID, activityKindCustom, "subagent_started", uuidToString(inboxEventID), lineage).Scan(&exists)
	if err != nil {
		slog.Warn("provider subagent activity: duplicate lookup failed",
			"agent_id", uuidToString(agentID),
			"inbox_event_id", uuidToString(inboxEventID),
			"error", err)
		return false
	}
	return exists
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

// cursorSubagentStartedMessage matches bubble title preference: description /
// name / subagent_type / short prompt / tool name.
func cursorSubagentStartedMessage(tool string, input map[string]any) string {
	title := ""
	if input != nil {
		for _, key := range []string{"description", "name", "subagent_type"} {
			if v, ok := input[key].(string); ok && strings.TrimSpace(v) != "" {
				title = strings.TrimSpace(v)
				break
			}
		}
		if title == "" {
			if v, ok := input["prompt"].(string); ok && strings.TrimSpace(v) != "" {
				p := strings.TrimSpace(v)
				if len(p) > 80 {
					p = p[:80]
				}
				title = p
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

func (h *Handler) maybeRecordCursorSubagentStarted(
	ctx context.Context,
	event db.AgentInboxEvent,
	runtimeID, deliveryID pgtype.UUID,
	msg TaskMessageRequest,
	baseDetails map[string]any,
) {
	if h == nil || h.DB == nil {
		return
	}
	tool := strings.TrimSpace(msg.Tool)
	callID := strings.TrimSpace(msg.CallID)
	dedupeKey := callID
	if dedupeKey == "" {
		dedupeKey = fmt.Sprintf("seq:%d:%s", msg.Seq, tool)
	}
	if h.cursorSubagentStartedExists(ctx, event.WorkspaceID, event.AgentID, event.ID, dedupeKey) {
		return
	}
	targetKind, targetID := agentInboxActivityTarget(event)
	details := map[string]any{
		"inbox_event_id":    uuidToString(event.ID),
		"source_message_id": uuidToString(event.SourceMessageID),
		"tool":              tool,
		"cursor_subagent":   true,
		"dedupe_key":        dedupeKey,
	}
	if deliveryID.Valid {
		details["delivery_id"] = uuidToString(deliveryID)
	}
	if callID != "" {
		details["call_id"] = callID
	}
	if baseDetails != nil {
		if seq, ok := baseDetails["seq"]; ok {
			details["seq"] = seq
		}
	}
	if msg.Input != nil {
		if st, ok := msg.Input["subagent_type"].(string); ok && strings.TrimSpace(st) != "" {
			details["subagent_type"] = strings.TrimSpace(st)
		}
	}
	h.recordAgentActivityEvent(ctx, h.DB,
		event.WorkspaceID, event.AgentID, runtimeID, pgtype.UUID{},
		activityKindCustom, "subagent_started", "info",
		targetKind, targetID, "",
		"cursor_tool", cursorSubagentStartedMessage(tool, msg.Input),
		details,
	)
}

func (h *Handler) cursorSubagentStartedExists(ctx context.Context, workspaceID, agentID, inboxEventID pgtype.UUID, dedupeKey string) bool {
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
    AND COALESCE(details->>'dedupe_key', '') = $6
)
`, workspaceID, agentID, activityKindCustom, "subagent_started", uuidToString(inboxEventID), dedupeKey).Scan(&exists)
	if err != nil {
		slog.Warn("cursor subagent activity: duplicate lookup failed",
			"agent_id", uuidToString(agentID),
			"inbox_event_id", uuidToString(inboxEventID),
			"error", err)
		return false
	}
	return exists
}

func agentInboxActivityMessageKind(messageType string) (kind, eventType, severity string) {
	if kind, eventType, _, ok := taskMessageCompactionActivity(messageType); ok {
		return kind, eventType, "info"
	}
	switch messageType {
	case "thinking":
		// Provider reasoning remains diagnostic-only. Empty thinking is handled
		// earlier as a legacy phase row, but current daemons do not send it.
		return activityKindThinking, "runtime_thinking", "info"
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
	if _, _, message, ok := taskMessageCompactionActivity(msg.Type); ok {
		return message
	}
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

func (h *Handler) recordTaskVisibleOutputActivity(ctx context.Context, workspaceID pgtype.UUID, task db.AgentInboxEvent, req TaskCompleteRequest) {
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

func (h *Handler) taskActivityTarget(ctx context.Context, task db.AgentInboxEvent) (string, pgtype.UUID, string) {
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

	loadWorkspaceRepos := func() {
		if len(resp.Repos) > 0 || resp.ProvisionManagedWorkdir {
			return
		}
		if workspace, err := h.Queries.GetWorkspace(ctx, event.WorkspaceID); err == nil && workspace.Repos != nil {
			var repos []RepoData
			if json.Unmarshal(workspace.Repos, &repos) == nil {
				resp.Repos = repos
			}
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
		resources, repos := h.mapProjectResources(ctx, projectID)
		resp.ProjectResources = resources
		resp.Repos = repos
		if len(resources) == 0 {
			resp.ProvisionManagedWorkdir = true
			resp.ManagedWorkdirRelPath = managedWorkdirRelPath(resp.ProjectID)
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
		loadWorkspaceRepos()
		return true
	}

	// Autopilot runtime tables dropped (LRM-1051). Historical AutopilotRunID
	// cannot hydrate title/payload; fall through to other task kinds.
	if event.AutopilotRunID.Valid {
		loadWorkspaceRepos()
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
		loadWorkspaceRepos()
		return strings.TrimSpace(resp.QuickCreatePrompt) != ""
	}

	// Environment dispatch, memory curation, Reminder, and other internal
	// wake kinds carry their exact prompt/config in the canonical context.
	resp.Kind = event.Reason
	resp.ThreadName = event.TriggerSummary.String
	loadWorkspaceRepos()
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
		resources, repos := h.mapProjectResources(ctx, channelProjectID)
		resp.ProjectResources = resources
		resp.Repos = repos
	}
	if len(resp.Repos) == 0 {
		if workspace, err := h.Queries.GetWorkspace(ctx, event.WorkspaceID); err == nil && workspace.Repos != nil {
			var repos []RepoData
			if json.Unmarshal(workspace.Repos, &repos) == nil {
				resp.Repos = repos
			}
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
