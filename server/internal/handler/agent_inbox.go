package handler

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

const agentInboxDrainMessageLimit = 50

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

type AckAgentInboxEventRequest struct {
	DeliveryID  string `json:"delivery_id"`
	LeaseToken  string `json:"lease_token"`
	SeenUpToSeq int64  `json:"seen_up_to_seq"`
}

type RenewAgentInboxEventRequest struct {
	DeliveryID string `json:"delivery_id"`
	LeaseToken string `json:"lease_token"`
}

type FailAgentInboxEventRequest struct {
	DeliveryID    string `json:"delivery_id"`
	LeaseToken    string `json:"lease_token"`
	Error         string `json:"error"`
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
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit inbox completion")
		return
	}
	if chatDonePayload != nil {
		h.publishAgentInboxChatDone(event, *chatDonePayload)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "acked_seq": acked.SeqTo})
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

	failureReason := strings.TrimSpace(req.FailureReason)
	reasonCode := strings.TrimSpace(req.ReasonCode)
	if reasonCode == "" {
		reasonCode = failureReason
	}
	if strings.Contains(errText, agent.ProviderAuthRequiredMarker) {
		reasonCode = agent.ProviderAuthRequiredMarker
	}
	delivery, err := h.Queries.GetAgentEventDelivery(r.Context(), deliveryID)
	if err != nil {
		slog.Warn("agent inbox fail: failed to reload delivery for activity event", "delivery_id", uuidToString(deliveryID), "error", err)
	}
	targetKind := "agent"
	targetID := failed.AgentID
	if failed.ChannelID.Valid {
		targetKind = "channel"
		targetID = failed.ChannelID
	} else if failed.ChatSessionID.Valid {
		targetKind = "dm"
		targetID = failed.ChatSessionID
	}
	recordAgentActivityEvent(r.Context(), h.DB,
		failed.WorkspaceID, failed.AgentID, delivery.RuntimeID, pgtype.UUID{},
		"lifecycle", "agent_inbox_failed", "error",
		targetKind, targetID, "",
		reasonCode, "Agent inbox delivery failed: "+truncateForActivity(errText, 200),
		map[string]any{
			"failure_reason":    failureReason,
			"reason_code":       reasonCode,
			"inbox_event_id":    uuidToString(failed.ID),
			"delivery_id":       uuidToString(deliveryID),
			"source_message_id": uuidToString(failed.SourceMessageID),
		},
	)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
	if agent, err := h.Queries.GetAgent(ctx, event.AgentID); err == nil {
		skills := h.TaskService.LoadAgentSkills(ctx, event.AgentID)
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
	if runtime.OwnerID.Valid {
		if owner, err := h.Queries.GetUser(ctx, runtime.OwnerID); err == nil {
			resp.RequestingUserName = userDisplayName(owner)
			resp.RequestingUserProfileDescription = owner.ProfileDescription
		}
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
	h.populateAgentInboxChatContext(ctx, event, &resp)
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

func (h *Handler) runtimeIDForAgentInboxDelivery(ctx context.Context, deliveryID pgtype.UUID) pgtype.UUID {
	var runtimeID pgtype.UUID
	_ = h.DB.QueryRow(ctx, `
		SELECT runtime_id
		FROM agent_event_delivery
		WHERE id = $1`, deliveryID).Scan(&runtimeID)
	return runtimeID
}

func (h *Handler) populateAgentInboxChatContext(ctx context.Context, event db.AgentInboxEvent, resp *AgentTaskResponse) {
	if !event.ChatSessionID.Valid {
		return
	}
	cs, err := h.Queries.GetChatSession(ctx, event.ChatSessionID)
	if err != nil {
		return
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
	if cs.SessionID.Valid && cs.RuntimeID.Valid {
		resp.PriorSessionID = cs.SessionID.String
	}
	if cs.WorkDir.Valid {
		resp.PriorWorkDir = cs.WorkDir.String
	}
	if prior, err := h.Queries.GetLastChatTaskSession(ctx, cs.ID); err == nil && prior.SessionID.Valid {
		if resp.PriorSessionID == "" {
			resp.PriorSessionID = prior.SessionID.String
		}
		if prior.WorkDir.Valid && resp.PriorWorkDir == "" {
			resp.PriorWorkDir = prior.WorkDir.String
		}
	}
	if event.SourceMessageID.Valid {
		h.populateAgentInboxInitiator(ctx, event.SourceMessageID, resp)
	}
	if msgs, err := h.Queries.ListChatMessages(ctx, cs.ID); err == nil && len(msgs) > 0 {
		unanswered := trailingUserMessages(msgs)
		parts := make([]string, 0, len(unanswered))
		for _, m := range unanswered {
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, m.Content)
			}
			if atts, attErr := h.Queries.ListAttachmentsByChatMessage(ctx, db.ListAttachmentsByChatMessageParams{
				ChatMessageID: m.ID,
				WorkspaceID:   cs.WorkspaceID,
			}); attErr == nil && len(atts) > 0 {
				for _, a := range atts {
					resp.ChatMessageAttachments = append(resp.ChatMessageAttachments, ChatAttachmentMeta{
						ID:          uuidToString(a.ID),
						Filename:    a.Filename,
						ContentType: a.ContentType,
					})
				}
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
	}
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
