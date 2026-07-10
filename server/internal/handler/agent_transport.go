package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/messageparts"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	agentTransportActionSend   = "message_send"
	agentTransportActionReact  = "message_react"
	agentTransportActionRead   = "message_read"
	agentTransportActionSearch = "message_search"
)

type AgentTransportSendRequest struct {
	Target          string                      `json:"target"`
	Content         string                      `json:"content"`
	Parts           []protocol.MessagePart      `json:"parts"`
	AttachmentIDs   []string                    `json:"attachment_ids"`
	Options         *protocol.ChatOutputOptions `json:"options,omitempty"`
	ClientMessageID string                      `json:"client_message_id"`
}

type AgentTransportSendResponse struct {
	Action      string                 `json:"action"`
	Target      string                 `json:"target"`
	Message     ChannelMessageResponse `json:"message"`
	Created     bool                   `json:"created"`
	TransportID string                 `json:"transport_id"`
}

type AgentTransportReactRequest struct {
	Target          string `json:"target"`
	MessageID       string `json:"message_id"`
	Emoji           string `json:"emoji"`
	ClientMessageID string `json:"client_message_id"`
}

type AgentTransportReactResponse struct {
	Action      string                  `json:"action"`
	Target      string                  `json:"target"`
	Reaction    ChannelReactionResponse `json:"reaction"`
	TransportID string                  `json:"transport_id"`
}

type AgentTransportReadRequest struct {
	Target string `json:"target"`
	Limit  int    `json:"limit"`
}

type AgentTransportReadResponse struct {
	Action      string                   `json:"action"`
	Target      string                   `json:"target"`
	ChannelID   string                   `json:"channel_id"`
	Messages    []ChannelMessageResponse `json:"messages"`
	Limit       int                      `json:"limit"`
	TransportID string                   `json:"transport_id"`
}

type AgentTransportSearchRequest struct {
	Target string `json:"target"`
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
}

type AgentTransportSearchResponse struct {
	Action      string                       `json:"action"`
	Target      string                       `json:"target"`
	ChannelID   string                       `json:"channel_id"`
	Query       string                       `json:"query"`
	Total       int                          `json:"total"`
	Results     []ChannelMessageSearchResult `json:"results"`
	TransportID string                       `json:"transport_id"`
}

type agentTransportTarget struct {
	kind                chatOutputTargetKind
	channel             ChannelResponse
	threadRoot          ChannelMessageResponse
	threadRootMessageID pgtype.UUID
	threadID            *string
	triggerDepth        int
	mainTimelineVisible bool
	raw                 string
}

type agentTransportSource struct {
	task         db.AgentTaskQueue
	origin       chatOutputOrigin
	inboxEventID pgtype.UUID
}

func (h *Handler) AgentTransportSendMessage(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	content, parts, err := messageparts.Normalize(req.Content, req.Parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, req.AttachmentIDs, "attachment_ids")
	if !ok {
		return
	}
	if strings.TrimSpace(content) == "" && len(parts) == 0 && len(attachmentIDs) == 0 {
		writeError(w, http.StatusBadRequest, "content, sticker, or attachment is required")
		return
	}
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	clientMessageID := strings.TrimSpace(req.ClientMessageID)
	if clientMessageID == "" {
		writeError(w, http.StatusBadRequest, "client_message_id is required")
		return
	}
	if len([]rune(clientMessageID)) > channelClientMessageIDMaxLen {
		writeError(w, http.StatusBadRequest, "client_message_id is too long")
		return
	}
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, req.Options, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	initiatorID := h.channelInitiatorForChatSession(r.Context(), source.task.ChatSessionID)
	msg, created, transportID, err := h.createAgentTransportMessage(r.Context(), source, target, content, parts, attachmentIDs, clientMessageID, initiatorID)
	if err != nil {
		if errors.Is(err, errChannelClientMessageConflict) {
			writeError(w, http.StatusConflict, "client_message_id conflicts with an existing channel message")
			return
		}
		slog.Warn("agent transport send failed", "task_id", uuidToString(source.task.ID), "agent_id", uuidToString(source.task.AgentID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to send message")
		return
	}
	writeJSON(w, http.StatusCreated, AgentTransportSendResponse{
		Action:      agentTransportActionSend,
		Target:      target.raw,
		Message:     msg,
		Created:     created,
		TransportID: transportID,
	})

	// Record a message-sent activity event (agent replied via multica send).
	recordAgentActivityEvent(r.Context(), h.DB,
		source.origin.workspaceID, source.task.AgentID, source.task.RuntimeID, nullableTaskIDForTransportSource(source),
		"lifecycle", "message_sent", "info",
		"channel", parseUUID(target.channel.ID), target.raw,
		"", "Agent sent a visible message",
		map[string]any{
			"message_id": msg.ID,
			"created":    created,
		},
	)
}

func (h *Handler) AgentTransportReactMessage(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportReactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	emoji := strings.TrimSpace(req.Emoji)
	if emoji == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	clientMessageID := strings.TrimSpace(req.ClientMessageID)
	if clientMessageID == "" {
		writeError(w, http.StatusBadRequest, "client_message_id is required")
		return
	}
	if len([]rune(clientMessageID)) > channelClientMessageIDMaxLen {
		writeError(w, http.StatusBadRequest, "client_message_id is too long")
		return
	}
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, nil, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	messageID := pgtype.UUID{}
	if raw := strings.TrimSpace(req.MessageID); raw != "" && !strings.EqualFold(raw, "CURRENT_MESSAGE") {
		parsed, ok := parseUUIDOrBadRequest(w, raw, "message_id")
		if !ok {
			return
		}
		messageID = parsed
	} else {
		messageID = h.channelReactionTargetFromPrompt(r.Context(), source.task.ChatSessionID, source.task.ID)
		if !messageID.Valid {
			messageID = target.threadRootMessageID
		}
	}
	if !messageID.Valid {
		writeError(w, http.StatusBadRequest, "message_id is required")
		return
	}
	reaction, transportID, err := h.createAgentTransportReaction(r.Context(), source, target, messageID, emoji, clientMessageID)
	if err != nil {
		slog.Warn("agent transport react failed", "task_id", uuidToString(source.task.ID), "agent_id", uuidToString(source.task.AgentID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to react to message")
		return
	}
	if reaction.ID == "" {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	writeJSON(w, http.StatusCreated, AgentTransportReactResponse{
		Action:      agentTransportActionReact,
		Target:      target.raw,
		Reaction:    reaction,
		TransportID: transportID,
	})

	// Record a reaction activity event (covers greeting/ack reaction-only replies).
	recordAgentActivityEvent(r.Context(), h.DB,
		source.origin.workspaceID, source.task.AgentID, source.task.RuntimeID, nullableTaskIDForTransportSource(source),
		"lifecycle", "reaction_sent", "info",
		"channel", parseUUID(target.channel.ID), target.raw,
		"", "Reacted "+emoji+" to a message",
		map[string]any{
			"emoji":      emoji,
			"message_id": uuidToString(messageID),
		},
	)
}

func (h *Handler) AgentTransportReadMessages(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	limit := clampAgentTransportLimit(req.Limit, 20)
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, nil, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	messages := h.readAgentTransportMessages(r.Context(), target, limit)
	h.decorateAgentTransportMessages(r.Context(), target.channel.WorkspaceID, messages)
	messageIDs := channelMessageIDs(messages)
	transportID, err := h.recordAgentTransportAudit(r.Context(), source, agentTransportActionRead, target.raw, parseUUID(target.channel.ID), pgtype.UUID{}, "", map[string]any{
		"channel_id":  target.channel.ID,
		"message_ids": messageIDs,
		"limit":       limit,
	})
	if err != nil {
		slog.Warn("agent transport read audit failed", "task_id", uuidToString(source.task.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to record message read")
		return
	}
	writeJSON(w, http.StatusOK, AgentTransportReadResponse{
		Action:      agentTransportActionRead,
		Target:      target.raw,
		ChannelID:   target.channel.ID,
		Messages:    messages,
		Limit:       limit,
		TransportID: transportID,
	})
}

func (h *Handler) AgentTransportSearchMessages(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req AgentTransportSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	limit := clampAgentTransportLimit(req.Limit, 50)
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, nil, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid target")
		return
	}
	total, results, err := h.searchAgentTransportMessages(r.Context(), target, query, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search messages")
		return
	}
	resultIDs := make([]string, 0, len(results))
	for _, result := range results {
		resultIDs = append(resultIDs, result.MessageID)
	}
	transportID, err := h.recordAgentTransportAudit(r.Context(), source, agentTransportActionSearch, target.raw, parseUUID(target.channel.ID), pgtype.UUID{}, "", map[string]any{
		"channel_id":   target.channel.ID,
		"query":        query,
		"result_ids":   resultIDs,
		"result_count": len(results),
		"total":        total,
		"limit":        limit,
	})
	if err != nil {
		slog.Warn("agent transport search audit failed", "task_id", uuidToString(source.task.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to record message search")
		return
	}
	writeJSON(w, http.StatusOK, AgentTransportSearchResponse{
		Action:      agentTransportActionSearch,
		Target:      target.raw,
		ChannelID:   target.channel.ID,
		Query:       query,
		Total:       total,
		Results:     results,
		TransportID: transportID,
	})
}

func (h *Handler) requireAgentTransportSource(w http.ResponseWriter, r *http.Request) (agentTransportSource, bool) {
	switch r.Header.Get("X-Actor-Source") {
	case "task_token":
		task, origin, ok := h.requireAgentTransportTask(w, r)
		if !ok {
			return agentTransportSource{}, false
		}
		return agentTransportSource{task: task, origin: origin}, true
	case "agent_inbox_token":
		task, origin, inboxEventID, ok := h.requireAgentTransportInboxEvent(w, r)
		if !ok {
			return agentTransportSource{}, false
		}
		return agentTransportSource{task: task, origin: origin, inboxEventID: inboxEventID}, true
	default:
		writeError(w, http.StatusForbidden, "agent transport requires a task token")
		return agentTransportSource{}, false
	}
}

func (h *Handler) requireAgentTransportTask(w http.ResponseWriter, r *http.Request) (db.AgentTaskQueue, chatOutputOrigin, bool) {
	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, http.StatusForbidden, "agent transport requires a task token")
		return db.AgentTaskQueue{}, chatOutputOrigin{}, false
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.AgentTaskQueue{}, chatOutputOrigin{}, false
	}
	agentID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
	if !ok {
		return db.AgentTaskQueue{}, chatOutputOrigin{}, false
	}
	taskID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "task id")
	if !ok {
		return db.AgentTaskQueue{}, chatOutputOrigin{}, false
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil || task.AgentID != agentID {
		writeError(w, http.StatusForbidden, "task token does not match this agent task")
		return db.AgentTaskQueue{}, chatOutputOrigin{}, false
	}
	if task.Status != "running" && task.Status != "dispatched" && task.Status != "waiting_local_directory" {
		writeError(w, http.StatusConflict, "agent task is not active")
		return db.AgentTaskQueue{}, chatOutputOrigin{}, false
	}
	origin, ok := h.chatOutputOriginForTask(r.Context(), task)
	if !ok || origin.workspaceID != wsUUID || origin.agentID != agentID {
		writeError(w, http.StatusForbidden, "agent task is not a channel task")
		return db.AgentTaskQueue{}, chatOutputOrigin{}, false
	}
	return task, origin, true
}

func (h *Handler) requireAgentTransportInboxEvent(w http.ResponseWriter, r *http.Request) (db.AgentTaskQueue, chatOutputOrigin, pgtype.UUID, bool) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.AgentTaskQueue{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	agentID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent id")
	if !ok {
		return db.AgentTaskQueue{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	eventID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-Inbox-Event-ID"), "inbox event id")
	if !ok {
		return db.AgentTaskQueue{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-Inbox-Delivery-ID"), "delivery id")
	if !ok {
		return db.AgentTaskQueue{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	event, err := h.Queries.GetAgentInboxEvent(r.Context(), eventID)
	if err != nil || event.AgentID != agentID || event.WorkspaceID != wsUUID || !event.ChatSessionID.Valid {
		writeError(w, http.StatusForbidden, "inbox token does not match this agent event")
		return db.AgentTaskQueue{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	var deliveryActive bool
	if err := h.DB.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1
			FROM agent_event_delivery d
			WHERE d.id = $1
			  AND d.inbox_event_id = $2
			  AND d.status IN ('leased', 'processing')
			  AND d.lease_expires_at > now()
		)`, deliveryID, event.ID).Scan(&deliveryActive); err != nil || !deliveryActive {
		writeError(w, http.StatusConflict, "agent inbox delivery is not active")
		return db.AgentTaskQueue{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	task := agentInboxSyntheticTask(event, h.runtimeIDForAgentInboxDelivery(r.Context(), deliveryID))
	origin, ok := h.chatOutputOriginForTask(r.Context(), task)
	if !ok || origin.workspaceID != wsUUID || origin.agentID != agentID {
		writeError(w, http.StatusForbidden, "agent inbox event is not a channel task")
		return db.AgentTaskQueue{}, chatOutputOrigin{}, pgtype.UUID{}, false
	}
	return task, origin, event.ID, true
}

func (h *Handler) resolveAgentTransportTarget(ctx context.Context, task db.AgentTaskQueue, origin chatOutputOrigin, rawTarget string, options *protocol.ChatOutputOptions, createDM bool) (agentTransportTarget, error) {
	rawTarget = strings.TrimSpace(rawTarget)
	if rawTarget == "" {
		if chatOutputOptionsPresent(options) {
			return agentTransportTarget{}, errChatOutputInvalidTarget
		}
		ch, found := h.getChannel(ctx, uuidToString(origin.workspaceID), origin.channelID)
		if !found || ch.ArchivedAt != nil {
			return agentTransportTarget{}, errChatOutputInvalidTarget
		}
		out := agentTransportTarget{kind: chatOutputTargetChannel, channel: ch, raw: ""}
		if task.ChatSessionID.Valid {
			threadID, threadRootMessageID, depth := h.channelThreadForChatTask(ctx, task.ChatSessionID, task.ID)
			if threadRootMessageID.Valid {
				root, err := h.loadChannelThreadRootForOutputTarget(ctx, origin.workspaceID, origin.channelID, threadRootMessageID)
				if err != nil {
					return agentTransportTarget{}, errChatOutputInvalidTarget
				}
				if threadID == nil || strings.TrimSpace(*threadID) == "" {
					fresh := uuid.NewString()
					threadID = &fresh
				}
				out.kind = chatOutputTargetThread
				out.threadRoot = root
				out.threadRootMessageID = threadRootMessageID
				out.threadID = threadID
				out.triggerDepth = depth + 1
			}
		}
		return out, nil
	}
	resolved, err := h.resolveChatOutputTarget(ctx, origin, rawTarget, options)
	if err != nil {
		return agentTransportTarget{}, err
	}
	out := agentTransportTarget{kind: resolved.kind, channel: resolved.channel, threadRoot: resolved.threadRoot, raw: rawTarget}
	switch resolved.kind {
	case chatOutputTargetDM:
		ch, ok := h.agentHumanDMChannel(ctx, origin.workspaceID, origin.agentID, resolved.recipientID, createDM)
		if !ok {
			return agentTransportTarget{}, errChatOutputInvalidTarget
		}
		out.channel = ch
	case chatOutputTargetThread:
		threadID := resolved.threadRoot.ThreadID
		if threadID == nil || strings.TrimSpace(*threadID) == "" {
			fresh := uuid.NewString()
			threadID = &fresh
		}
		out.threadRootMessageID = parseUUID(resolved.threadRoot.ID)
		out.threadID = threadID
		out.triggerDepth = resolved.threadRoot.TriggerDepth + 1
		out.mainTimelineVisible = options.ShowInChannelValue()
	}
	return out, nil
}

func (h *Handler) agentHumanDMChannel(ctx context.Context, workspaceID, agentID, userID pgtype.UUID, create bool) (ChannelResponse, bool) {
	workspaceIDText := uuidToString(workspaceID)
	canonical := dmCanonicalName("user", uuidToString(userID), "agent", uuidToString(agentID))
	if ch, found := h.findDMChannel(ctx, workspaceIDText, canonical); found {
		if create {
			h.clearDMPeerHidden(ctx, workspaceIDText, uuidToString(userID), dmPeerRef{Type: "agent", ID: agentID})
		}
		return ch, true
	}
	if !create {
		return ChannelResponse{}, false
	}
	return h.ensureAgentHumanDMChannel(ctx, workspaceID, agentID, userID)
}

func (h *Handler) createAgentTransportMessage(ctx context.Context, source agentTransportSource, target agentTransportTarget, content string, parts []protocol.MessagePart, attachmentIDs []pgtype.UUID, clientMessageID string, initiatorID pgtype.UUID) (ChannelMessageResponse, bool, string, error) {
	agentName := h.agentName(ctx, source.origin.agentID)
	input := channelMessageInsertInput{
		ChannelID:           parseUUID(target.channel.ID),
		WorkspaceID:         source.origin.workspaceID,
		AuthorID:            source.origin.agentID,
		AuthorName:          agentName,
		Content:             content,
		Parts:               parts,
		ThreadRootMessageID: target.threadRootMessageID,
		ThreadID:            target.threadID,
		TriggerDepth:        target.triggerDepth,
		ClientMessageID:     &clientMessageID,
		MainTimelineVisible: target.mainTimelineVisible,
	}
	msg, created, transportID, err := h.insertAgentTransportMessageWithAudit(ctx, source, target, input, attachmentIDs, clientMessageID)
	if err != nil {
		return ChannelMessageResponse{}, false, "", err
	}
	if len(attachmentIDs) > 0 {
		msg.Attachments = h.groupChannelMessageAttachments(ctx, uuidToString(source.origin.workspaceID), []pgtype.UUID{parseUUID(msg.ID)})[msg.ID]
	}
	if target.mainTimelineVisible {
		messages := []ChannelMessageResponse{msg}
		h.attachChannelMessageThreadRootSummaries(ctx, target.channel.WorkspaceID, messages)
		msg = messages[0]
	}
	if created {
		_, _ = h.DB.Exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, input.ChannelID)
		if target.channel.Kind == "dm" {
			h.clearDMHiddenForChannelMembers(ctx, target.channel.WorkspaceID, input.ChannelID)
		}
		h.publishChannelToMembers(ctx, protocol.EventChannelMessage, target.channel.WorkspaceID, "agent", uuidToString(source.origin.agentID), input.ChannelID, msg)
		if target.channel.Kind == "group" {
			if target.threadRootMessageID.Valid {
				h.dispatchChannelThreadReplyMentions(ctx, target.channel, msg, initiatorID)
			} else {
				h.dispatchChannelMentions(ctx, target.channel, msg, initiatorID)
			}
			h.sendChannelMessageToFeishu(ctx, target.channel, agentName, content)
		}
	}
	return msg, created, transportID, nil
}

func (h *Handler) insertAgentTransportMessageWithAudit(ctx context.Context, source agentTransportSource, target agentTransportTarget, input channelMessageInsertInput, attachmentIDs []pgtype.UUID, clientMessageID string) (ChannelMessageResponse, bool, string, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return ChannelMessageResponse{}, false, "", err
	}
	msg, err := insertChannelMessageWithPartsExec(ctx, tx, input.ChannelID, input.WorkspaceID, "agent", input.AuthorID, input.AuthorName, input.Content, input.Parts, "multica", nil, input.ClientMessageID, pgtype.UUID{}, pgtype.UUID{}, nil, input.ThreadRootMessageID, input.ThreadID, input.TriggerDepth, input.MainTimelineVisible)
	if err != nil {
		_ = tx.Rollback(ctx)
		if isUniqueViolation(err) {
			return h.resolveDuplicateAgentTransportMessage(ctx, source, target, input, clientMessageID)
		}
		return ChannelMessageResponse{}, false, "", err
	}
	if len(attachmentIDs) > 0 {
		qtx := h.Queries.WithTx(tx)
		if err := qtx.LinkOwnedAttachmentsToChannelMessage(ctx, db.LinkOwnedAttachmentsToChannelMessageParams{
			ChannelID:        input.ChannelID,
			ChannelMessageID: parseUUID(msg.ID),
			WorkspaceID:      source.origin.workspaceID,
			UploaderType:     "agent",
			UploaderID:       source.origin.agentID,
			AttachmentIds:    attachmentIDs,
		}); err != nil {
			_ = tx.Rollback(ctx)
			return ChannelMessageResponse{}, false, "", err
		}
	}
	transportID, err := h.recordAgentTransportAuditExec(ctx, tx, source, agentTransportActionSend, target.raw, input.ChannelID, parseUUID(msg.ID), clientMessageID, map[string]any{
		"channel_id":        target.channel.ID,
		"message_id":        msg.ID,
		"client_message_id": clientMessageID,
		"created":           true,
		"seq":               msg.Seq,
		"thread_root_id":    msg.ThreadRootMessageID,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		return ChannelMessageResponse{}, false, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChannelMessageResponse{}, false, "", err
	}
	return msg, true, transportID, nil
}

func (h *Handler) resolveDuplicateAgentTransportMessage(ctx context.Context, source agentTransportSource, target agentTransportTarget, input channelMessageInsertInput, clientMessageID string) (ChannelMessageResponse, bool, string, error) {
	existing, found, err := h.findAgentChannelMessageByClientID(ctx, input.WorkspaceID, input.ChannelID, input.AuthorID, clientMessageID)
	if err != nil {
		return ChannelMessageResponse{}, false, "", err
	}
	if !found {
		return ChannelMessageResponse{}, false, "", errChannelClientMessageConflict
	}
	ok, err := h.matchesChannelMessageIdempotencyPayload(ctx, existing, input, nil)
	if err != nil {
		return ChannelMessageResponse{}, false, "", err
	}
	if !ok {
		return ChannelMessageResponse{}, false, "", errChannelClientMessageConflict
	}
	transportID, err := h.recordAgentTransportAudit(ctx, source, agentTransportActionSend, target.raw, input.ChannelID, parseUUID(existing.ID), clientMessageID, map[string]any{
		"channel_id":        target.channel.ID,
		"message_id":        existing.ID,
		"client_message_id": clientMessageID,
		"created":           false,
		"seq":               existing.Seq,
		"thread_root_id":    existing.ThreadRootMessageID,
	})
	if err != nil {
		return ChannelMessageResponse{}, false, "", err
	}
	return existing, false, transportID, nil
}

func (h *Handler) findAgentChannelMessageByClientID(ctx context.Context, workspaceID, channelID, authorID pgtype.UUID, clientMessageID string) (ChannelMessageResponse, bool, error) {
	row := h.DB.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE workspace_id = $1 AND channel_id = $2 AND author_type = 'agent' AND author_id = $3 AND client_message_id = $4`,
		workspaceID, channelID, authorID, clientMessageID)
	msg, err := scanChannelMessage(row)
	if err != nil {
		if errorsIsNoRows(err) {
			return ChannelMessageResponse{}, false, nil
		}
		return ChannelMessageResponse{}, false, err
	}
	return msg, true, nil
}

func (h *Handler) createAgentTransportReaction(ctx context.Context, source agentTransportSource, target agentTransportTarget, messageID pgtype.UUID, emoji, clientMessageID string) (ChannelReactionResponse, string, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return ChannelReactionResponse{}, "", err
	}
	reaction, found, err := h.insertAgentChannelReaction(ctx, tx, parseUUID(target.channel.ID), source.origin.workspaceID, source.origin.agentID, messageID, emoji)
	if err != nil {
		_ = tx.Rollback(ctx)
		return ChannelReactionResponse{}, "", err
	}
	if !found {
		_ = tx.Rollback(ctx)
		return ChannelReactionResponse{}, "", nil
	}
	transportID, err := h.recordAgentTransportAuditExec(ctx, tx, source, agentTransportActionReact, target.raw, parseUUID(target.channel.ID), messageID, clientMessageID, map[string]any{
		"channel_id":        target.channel.ID,
		"message_id":        uuidToString(messageID),
		"reaction_id":       reaction.ID,
		"emoji":             emoji,
		"client_message_id": clientMessageID,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		return ChannelReactionResponse{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChannelReactionResponse{}, "", err
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelReactionAdded, target.channel.WorkspaceID, "agent", uuidToString(source.origin.agentID), parseUUID(target.channel.ID), map[string]any{"reaction": reaction, "channel_id": target.channel.ID, "message_id": uuidToString(messageID)})
	return reaction, transportID, nil
}

func (h *Handler) readAgentTransportMessages(ctx context.Context, target agentTransportTarget, limit int) []ChannelMessageResponse {
	if target.threadRootMessageID.Valid {
		return h.channelThreadContextMessages(ctx, target.channel.WorkspaceID, target.channel.ID, uuidToString(target.threadRootMessageID), limit)
	}
	return h.recentChannelMessages(ctx, target.channel.WorkspaceID, target.channel.ID, limit)
}

func (h *Handler) decorateAgentTransportMessages(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	messageIDs := make([]pgtype.UUID, len(messages))
	for i, msg := range messages {
		messageIDs[i] = parseUUID(msg.ID)
	}
	grouped := h.groupChannelMessageAttachments(ctx, workspaceID, messageIDs)
	for i := range messages {
		messages[i].Attachments = grouped[messages[i].ID]
	}
	h.attachChannelMessageReactions(ctx, workspaceID, messages)
	h.attachChannelMessageReplySummaries(ctx, workspaceID, messages)
	h.attachChannelMessageThreadRootSummaries(ctx, workspaceID, messages)
	applyChannelMessageTombstoneReadModel(messages)
}

func (h *Handler) searchAgentTransportMessages(ctx context.Context, target agentTransportTarget, query string, limit int) (int, []ChannelMessageSearchResult, error) {
	pattern := "%" + escapeLike(query) + "%"
	threadRootID := nullableUUID(target.threadRootMessageID)
	var total int
	if err := h.DB.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2 AND author_type <> 'system' AND deleted_at IS NULL AND content ILIKE $3 ESCAPE '\'
		  AND ($4::uuid IS NULL OR id = $4 OR thread_root_message_id = $4)`,
		parseUUID(target.channel.ID), parseUUID(target.channel.WorkspaceID), pattern, threadRootID).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, channel_id, thread_root_message_id, author_type, author_id, author_name, content, created_at
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2 AND author_type <> 'system' AND deleted_at IS NULL AND content ILIKE $3 ESCAPE '\'
		  AND ($4::uuid IS NULL OR id = $4 OR thread_root_message_id = $4)
		ORDER BY seq ASC
		LIMIT $5`, parseUUID(target.channel.ID), parseUUID(target.channel.WorkspaceID), pattern, threadRootID, limit)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	var results []ChannelMessageSearchResult
	for rows.Next() {
		var id, chID, rootID, authorID pgtype.UUID
		var authorType, authorName, content string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &chID, &rootID, &authorType, &authorID, &authorName, &content, &createdAt); err != nil {
			return 0, nil, err
		}
		results = append(results, ChannelMessageSearchResult{
			MessageID:           uuidToString(id),
			ChannelID:           uuidToString(chID),
			ThreadRootMessageID: uuidToPtr(rootID),
			Type:                authorType,
			AuthorID:            uuidToPtr(authorID),
			AuthorName:          authorName,
			Content:             content,
			CreatedAt:           timestampToString(createdAt),
		})
	}
	return total, results, rows.Err()
}

func (h *Handler) recordAgentTransportAudit(ctx context.Context, source agentTransportSource, action, target string, channelID, messageID pgtype.UUID, clientMessageID string, contextPack map[string]any) (string, error) {
	return h.recordAgentTransportAuditExec(ctx, h.DB, source, action, target, channelID, messageID, clientMessageID, contextPack)
}

func (h *Handler) recordAgentTransportAuditExec(ctx context.Context, exec dbExecutor, source agentTransportSource, action, target string, channelID, messageID pgtype.UUID, clientMessageID string, contextPack map[string]any) (string, error) {
	pack, err := json.Marshal(contextPack)
	if err != nil {
		return "", err
	}
	var auditID pgtype.UUID
	if err := exec.QueryRow(ctx, `
		INSERT INTO agent_task_transport_audit (workspace_id, task_id, inbox_event_id, agent_id, action, target, channel_id, channel_message_id, client_message_id, context_pack)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
		RETURNING id`,
		source.origin.workspaceID, nullableTaskIDForTransportSource(source), nullableInboxEventIDForTransportSource(source), source.task.AgentID, action, strings.TrimSpace(target), nullableUUID(channelID), nullableUUID(messageID), nullableAgentTransportClientID(clientMessageID), pack).Scan(&auditID); err != nil {
		return "", err
	}
	return uuidToString(auditID), nil
}

func nullableTaskIDForTransportSource(source agentTransportSource) pgtype.UUID {
	if source.inboxEventID.Valid {
		return pgtype.UUID{}
	}
	return source.task.ID
}

func nullableInboxEventIDForTransportSource(source agentTransportSource) pgtype.UUID {
	return source.inboxEventID
}

func nullableAgentTransportClientID(clientMessageID string) any {
	clientMessageID = strings.TrimSpace(clientMessageID)
	if clientMessageID == "" {
		return nil
	}
	return clientMessageID
}

func (h *Handler) taskHasAgentTransportVisibleOutput(ctx context.Context, taskID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_task_transport_audit
			WHERE task_id = $1 AND action IN ('message_send', 'message_react')
		)`, taskID).Scan(&exists)
	return err == nil && exists
}

func (h *Handler) inboxEventHasAgentTransportVisibleOutput(ctx context.Context, eventID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_task_transport_audit
			WHERE inbox_event_id = $1 AND action IN ('message_send', 'message_react')
		)`, eventID).Scan(&exists)
	return err == nil && exists
}

func channelMessageIDs(messages []ChannelMessageResponse) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.ID)
	}
	return out
}

func clampAgentTransportLimit(value, def int) int {
	if value <= 0 {
		value = def
	}
	if value > channelMessagesMaxLimit {
		return channelMessagesMaxLimit
	}
	return value
}
