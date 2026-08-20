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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const standaloneChatDeliveryTargetPrefix = "chat:"

func standaloneChatDeliveryID(messageID, agentID string) string {
	return "chat:" + messageID + ":agent:" + agentID
}

func standaloneChatDeliveryMessageID(deliveryID, agentID string) (string, bool) {
	const prefix = "chat:"
	suffix := ":agent:" + agentID
	if !strings.HasPrefix(deliveryID, prefix) || !strings.HasSuffix(deliveryID, suffix) {
		return "", false
	}
	messageID := strings.TrimSuffix(strings.TrimPrefix(deliveryID, prefix), suffix)
	if _, err := util.ParseUUID(messageID); err != nil {
		return "", false
	}
	return messageID, true
}

func (h *Handler) deliverStandaloneChatMessage(ctx context.Context, session db.ChatSession, msg db.ChatMessage, content string, parts []protocol.MessagePart, initiatorUserID string) string {
	if h == nil || !session.AgentID.Valid {
		return ""
	}
	agent, err := h.Queries.GetAgent(ctx, session.AgentID)
	if err != nil || !agent.RuntimeID.Valid {
		slog.Warn("standalone chat delivery skipped: agent runtime missing",
			"chat_session_id", uuidToString(session.ID),
			"agent_id", uuidToString(session.AgentID),
			"error", err,
		)
		return ""
	}
	agentID := uuidToString(agent.ID)
	messageID := uuidToString(msg.ID)
	sessionID := uuidToString(session.ID)
	deliveryID := standaloneChatDeliveryID(messageID, agentID)
	target := standaloneChatDeliveryTargetPrefix + sessionID
	deliverContent := content
	if prefix := h.buildNoteChatWakePrefix(ctx, session.ID); prefix != "" {
		deliverContent = prefix + content
	}
	var seq int64
	if err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(MAX(seq), 0) + 1
		FROM agent_chat_delivery
		WHERE chat_session_id = $1
	`, session.ID).Scan(&seq); err != nil {
		slog.Warn("standalone chat delivery seq failed", "chat_session_id", sessionID, "error", err)
		return ""
	}
	tag, err := h.DB.Exec(ctx, `
		INSERT INTO agent_chat_delivery (workspace_id, agent_id, chat_session_id, message_id, target, seq)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (agent_id, message_id) DO NOTHING`,
		session.WorkspaceID, agent.ID, session.ID, msg.ID, target, seq)
	if err != nil {
		slog.Warn("persist standalone chat delivery failed", "chat_session_id", sessionID, "message_id", messageID, "error", err)
		return ""
	}
	if tag.RowsAffected() != 1 {
		if err := h.DB.QueryRow(ctx, `
			SELECT seq FROM agent_chat_delivery
			WHERE agent_id = $1 AND message_id = $2`,
			agent.ID, msg.ID).Scan(&seq); err != nil {
			slog.Warn("reload standalone chat delivery seq failed", "delivery_id", deliveryID, "error", err)
			return deliveryID
		}
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID:    agentID,
		Target:     target,
		Seq:        seq,
		DeliveryID: deliveryID,
		Message: protocol.AgentMessageProjection{
			ID:            messageID,
			Target:        target,
			ReplyTarget:   target,
			Seq:           seq,
			Content:       deliverContent,
			Parts:         parts,
			InitiatorType: "member",
			InitiatorID:   initiatorUserID,
		},
	}
	h.attachCanonicalMessageMemories(ctx, uuidToString(session.WorkspaceID), agent.ID, &delivery.Message)
	h.notifyStandaloneChatDelivery(ctx, agent, delivery)
	// Same as a channel Message: do not fake an ACK. If the Agent has no
	// live resident process, start the desired launch so accept/redeliver
	// can proceed. Computer-offline stays deferred until Runner ready.
	h.reconcileConnectedRuntime(ctx, uuidToString(session.WorkspaceID), agent.RuntimeID)
	return deliveryID
}

type standaloneChatOutstanding struct {
	SessionID  string
	CreatedAt  string
	DeliveryID string
}

func standaloneChatOutstandingFromLastRole(role string) bool {
	return role == "user"
}

func (h *Handler) loadStandaloneChatOutstanding(ctx context.Context, sessionID pgtype.UUID) (standaloneChatOutstanding, bool, error) {
	if h == nil || h.DB == nil {
		return standaloneChatOutstanding{}, false, nil
	}
	var lastRole string
	var lastCreated time.Time
	err := h.DB.QueryRow(ctx, `
		SELECT role, created_at
		FROM chat_message
		WHERE chat_session_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, sessionID).Scan(&lastRole, &lastCreated)
	if err != nil {
		return standaloneChatOutstanding{}, false, nil
	}
	if !standaloneChatOutstandingFromLastRole(lastRole) {
		return standaloneChatOutstanding{}, false, nil
	}
	out := standaloneChatOutstanding{
		SessionID: uuidToString(sessionID),
		CreatedAt: lastCreated.UTC().Format(time.RFC3339Nano),
	}
	var agentID, messageID pgtype.UUID
	if qerr := h.DB.QueryRow(ctx, `
		SELECT agent_id, message_id
		FROM agent_chat_delivery
		WHERE chat_session_id = $1 AND acked_at IS NULL
		ORDER BY seq DESC, message_id DESC
		LIMIT 1`, sessionID).Scan(&agentID, &messageID); qerr == nil {
		out.DeliveryID = standaloneChatDeliveryID(uuidToString(messageID), uuidToString(agentID))
	}
	return out, true, nil
}

func (h *Handler) listStandaloneChatOutstanding(ctx context.Context, sessionIDs []pgtype.UUID) ([]standaloneChatOutstanding, error) {
	if h == nil || h.DB == nil || len(sessionIDs) == 0 {
		return nil, nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT DISTINCT ON (m.chat_session_id) m.chat_session_id, m.role, m.created_at
		FROM chat_message m
		WHERE m.chat_session_id = ANY($1)
		ORDER BY m.chat_session_id, m.created_at DESC, m.id DESC`, sessionIDs)
	if err != nil {
		return nil, err
	}
	// Drain the list cursor before the delivery lookup so nested QueryRow
	// cannot hold two pool connections (cursordeadlock / #1803).
	type pendingSession struct {
		id        pgtype.UUID
		createdAt time.Time
	}
	pending := make([]pendingSession, 0)
	for rows.Next() {
		var sessionID pgtype.UUID
		var role string
		var createdAt time.Time
		if err := rows.Scan(&sessionID, &role, &createdAt); err != nil {
			rows.Close()
			return nil, err
		}
		if !standaloneChatOutstandingFromLastRole(role) {
			continue
		}
		pending = append(pending, pendingSession{id: sessionID, createdAt: createdAt})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(pending) == 0 {
		return nil, nil
	}

	pendingIDs := make([]pgtype.UUID, len(pending))
	indexBySession := make(map[string]int, len(pending))
	out := make([]standaloneChatOutstanding, len(pending))
	for i, item := range pending {
		sid := uuidToString(item.id)
		pendingIDs[i] = item.id
		indexBySession[sid] = i
		out[i] = standaloneChatOutstanding{
			SessionID: sid,
			CreatedAt: item.createdAt.UTC().Format(time.RFC3339Nano),
		}
	}

	deliveryRows, err := h.DB.Query(ctx, `
		SELECT DISTINCT ON (chat_session_id) chat_session_id, agent_id, message_id
		FROM agent_chat_delivery
		WHERE chat_session_id = ANY($1) AND acked_at IS NULL
		ORDER BY chat_session_id, seq DESC, message_id DESC`, pendingIDs)
	if err != nil {
		return nil, err
	}
	defer deliveryRows.Close()
	for deliveryRows.Next() {
		var sessionID, agentID, messageID pgtype.UUID
		if err := deliveryRows.Scan(&sessionID, &agentID, &messageID); err != nil {
			return nil, err
		}
		idx, ok := indexBySession[uuidToString(sessionID)]
		if !ok {
			continue
		}
		out[idx].DeliveryID = standaloneChatDeliveryID(uuidToString(messageID), uuidToString(agentID))
	}
	return out, deliveryRows.Err()
}

func (h *Handler) insertStandaloneAssistantReply(ctx context.Context, session db.ChatSession, content string, parts []protocol.MessagePart) (db.ChatMessage, error) {
	normalized, normalizedParts, err := messageparts.Normalize(content, parts)
	if err != nil {
		return db.ChatMessage{}, err
	}
	assistant, err := h.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "assistant",
		Content:       normalized,
		Parts:         messageparts.MustJSON(normalizedParts),
	})
	if err != nil {
		return db.ChatMessage{}, err
	}
	if err := h.Queries.SetUnreadSinceIfNull(ctx, session.ID); err != nil {
		slog.Warn("failed to set unread_since on standalone chat reply", "session_id", uuidToString(session.ID), "error", err)
	}
	if err := h.Queries.TouchChatSession(ctx, session.ID); err != nil {
		slog.Warn("failed to touch chat session", "session_id", uuidToString(session.ID), "error", err)
	}
	h.publishChatToCreator(protocol.EventChatDone, uuidToString(session.WorkspaceID), "system", "", uuidToString(session.ID), uuidToString(session.CreatorID), protocol.ChatDonePayload{
		ChatSessionID: uuidToString(session.ID),
		Type:          protocol.ChatOutputKindMessage,
		MessageID:     uuidToString(assistant.ID),
		Content:       assistant.Content,
		Parts:         normalizedParts,
		CreatedAt:     timestampToString(assistant.CreatedAt),
	})
	return assistant, nil
}

func (h *Handler) ackStandaloneChatDeliveriesForSession(ctx context.Context, sessionID pgtype.UUID) {
	if h == nil || h.DB == nil {
		return
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE agent_chat_delivery
		SET acked_at = COALESCE(acked_at, now())
		WHERE chat_session_id = $1 AND acked_at IS NULL`, sessionID); err != nil {
		slog.Warn("ack standalone chat deliveries on cancel failed", "session_id", uuidToString(sessionID), "error", err)
	}
}

func (h *Handler) CancelStandaloneChat(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	sessionID := chi.URLParam(r, "sessionId")
	session, ok := h.gateChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return
	}
	out, outstanding, err := h.loadStandaloneChatOutstanding(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load outstanding chat")
		return
	}
	if !outstanding {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pending": false})
		return
	}
	_ = out
	assistant, err := h.Queries.CreateChatMessage(r.Context(), db.CreateChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "assistant",
		Content:       "",
		Parts:         messageparts.MustJSON(nil),
		FailureReason: pgtype.Text{String: "manual", Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel standalone chat")
		return
	}
	h.ackStandaloneChatDeliveriesForSession(r.Context(), session.ID)
	if err := h.Queries.TouchChatSession(r.Context(), session.ID); err != nil {
		slog.Warn("failed to touch chat session on standalone cancel", "session_id", sessionID, "error", err)
	}
	h.publishChatToCreator(protocol.EventChatDone, uuidToString(session.WorkspaceID), "system", "", uuidToString(session.ID), uuidToString(session.CreatorID), protocol.ChatDonePayload{
		ChatSessionID: uuidToString(session.ID),
		Type:          protocol.ChatOutputKindMessage,
		MessageID:     uuidToString(assistant.ID),
		Content:       assistant.Content,
		CreatedAt:     timestampToString(assistant.CreatedAt),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pending": false, "message_id": uuidToString(assistant.ID)})
}

func (h *Handler) ReportStandaloneChatAssistantReply(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionId")
	session, err := h.Queries.GetChatSession(r.Context(), parseUUID(sessionID))
	if err != nil {
		writeError(w, http.StatusNotFound, "chat session not found")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(session.WorkspaceID)) {
		return
	}
	var req struct {
		Content string                 `json:"content"`
		Parts   []protocol.MessagePart `json:"parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Content) == "" && len(req.Parts) == 0 {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	assistant, err := h.insertStandaloneAssistantReply(r.Context(), session, req.Content, req.Parts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create assistant reply")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message_id": uuidToString(assistant.ID)})
}

func (h *Handler) notifyStandaloneChatDelivery(ctx context.Context, agent db.Agent, delivery protocol.AgentDeliverPayload) {
	if h == nil || h.DB == nil || h.AgentDeliveryNotifier == nil || !agent.RuntimeID.Valid {
		return
	}
	var daemonID *string
	if err := h.DB.QueryRow(ctx, `SELECT daemon_id FROM agent_runtime WHERE id = $1`, agent.RuntimeID).Scan(&daemonID); err != nil {
		slog.Warn("load standalone chat delivery daemon failed", "agent_id", delivery.AgentID, "error", err)
		return
	}
	workspaceID := uuidToString(agent.WorkspaceID)
	if daemonID == nil || strings.TrimSpace(*daemonID) == "" || !h.AgentDeliveryNotifier.NotifyWorkspaceAgentDelivery(workspaceID, *daemonID, delivery) {
		slog.Debug("standalone chat live delivery deferred to recovery",
			"workspace_id", workspaceID,
			"agent_id", delivery.AgentID,
			"delivery_id", delivery.DeliveryID,
		)
	}
}

func (h *Handler) persistStandaloneChatDeliveryAck(ctx context.Context, identity daemonws.ClientIdentity, ack protocol.AgentDeliverAckPayload, messageID string) error {
	if h.TxStarter == nil {
		return errors.New("standalone chat delivery acknowledgement transaction unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE agent_chat_delivery
		SET acked_at = COALESCE(acked_at, now())
		WHERE workspace_id = $1 AND agent_id = $2 AND message_id = $3 AND seq = $4`,
		parseUUID(identity.WorkspaceID), parseUUID(ack.AgentID), parseUUID(messageID), ack.Seq)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("unknown standalone chat delivery acknowledgement")
	}
	return tx.Commit(ctx)
}

func (h *Handler) redeliverUnacknowledgedStandaloneChat(ctx context.Context, identity daemonws.ClientIdentity) error {
	if h == nil || h.DB == nil || h.AgentDeliveryNotifier == nil {
		return nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT delivery.agent_id, delivery.message_id, delivery.seq, delivery.target,
		       delivery.chat_session_id, m.content, m.parts
		FROM agent_chat_delivery delivery
		JOIN agent recipient ON recipient.id = delivery.agent_id AND recipient.archived_at IS NULL
		JOIN agent_runtime runtime ON runtime.id = recipient.runtime_id
		JOIN chat_message m ON m.id = delivery.message_id
		WHERE delivery.workspace_id = $1
		  AND runtime.daemon_id = $2
		  AND delivery.acked_at IS NULL
		ORDER BY delivery.seq, delivery.message_id`,
		parseUUID(identity.WorkspaceID), identity.DaemonID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var agentID, messageID, sessionID pgtype.UUID
		var seq int64
		var target, content string
		var rawParts []byte
		if err := rows.Scan(&agentID, &messageID, &seq, &target, &sessionID, &content, &rawParts); err != nil {
			return err
		}
		var parts []protocol.MessagePart
		if len(rawParts) > 0 && string(rawParts) != "null" {
			_ = json.Unmarshal(rawParts, &parts)
		}
		agentIDText := uuidToString(agentID)
		messageIDText := uuidToString(messageID)
		// Live deliverStandaloneChatMessage prefixes <note_chat_context> for
		// Notes FAB sessions. chat_message.content stays raw; redelivery must
		// rebuild the same prefix or the agent wakes without note root context.
		deliverContent := content
		if prefix := h.buildNoteChatWakePrefix(ctx, sessionID); prefix != "" && !strings.HasPrefix(content, "<note_chat_context>") {
			deliverContent = prefix + content
		}
		delivery := protocol.AgentDeliverPayload{
			AgentID:    agentIDText,
			Target:     target,
			Seq:        seq,
			DeliveryID: standaloneChatDeliveryID(messageIDText, agentIDText),
			Message: protocol.AgentMessageProjection{
				ID:          messageIDText,
				Target:      target,
				ReplyTarget: target,
				Seq:         seq,
				Content:     deliverContent,
				Parts:       parts,
			},
		}
		if !h.AgentDeliveryNotifier.NotifyWorkspaceAgentDelivery(identity.WorkspaceID, identity.DaemonID, delivery) {
			slog.Debug("standalone chat redelivery deferred", "delivery_id", delivery.DeliveryID)
		}
	}
	return rows.Err()
}
