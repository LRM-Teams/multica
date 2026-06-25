package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type AgentDirectMessageRequest struct {
	Content string `json:"content"`
}

// AgentDirectMessage lets an agent proactively send a 1:1 direct message to the
// human it is currently working for. DMs are strictly human <-> agent: an agent
// that needs to reach ANOTHER agent must post in a channel and @-mention it, so
// this endpoint refuses any non-human recipient.
//
// Auth: agent-only. The caller must be a task-scoped agent actor (resolveActor
// returns "agent"); a human/member request is rejected with 403. The recipient
// is the current task's initiator, falling back to the agent's owner.
func (h *Handler) AgentDirectMessage(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	actorType, actorID := h.resolveActor(r, "", workspaceID)
	if actorType != "agent" || actorID == "" {
		writeError(w, http.StatusForbidden, "only agents can send direct messages")
		return
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, actorID, "agent id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	var req AgentDirectMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	// Resolve the recipient: the human who triggered the current task, falling
	// back to the agent's owner. Must be a real workspace member (human).
	recipient, ok := h.resolveDMRecipient(r, wsUUID, agentUUID)
	if !ok {
		writeError(w, http.StatusBadRequest, "no human recipient for this DM — to reach another agent, post in a channel and @-mention it")
		return
	}

	// Find the most-recent active DM thread for (recipient, agent), or open one.
	var sessionID pgtype.UUID
	err := h.DB.QueryRow(r.Context(), `
		SELECT cs.id FROM chat_session cs
		WHERE cs.workspace_id = $1 AND cs.agent_id = $2 AND cs.creator_id = $3 AND cs.status = 'active'
		  AND NOT EXISTS (SELECT 1 FROM channel_agent_session cas WHERE cas.chat_session_id = cs.id)
		ORDER BY cs.updated_at DESC LIMIT 1`, wsUUID, agentUUID, recipient).Scan(&sessionID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "failed to resolve dm session")
			return
		}
		session, cerr := h.Queries.CreateChatSession(r.Context(), db.CreateChatSessionParams{
			WorkspaceID: wsUUID,
			AgentID:     agentUUID,
			CreatorID:   recipient,
			Title:       dmSessionTitle(content),
		})
		if cerr != nil {
			writeError(w, http.StatusInternalServerError, "failed to create dm session")
			return
		}
		sessionID = session.ID
	}

	var taskID pgtype.UUID
	if tid := r.Header.Get("X-Task-ID"); tid != "" {
		if parsed, perr := util.ParseUUID(tid); perr == nil {
			taskID = parsed
		}
	}

	msg, err := h.Queries.CreateChatMessage(r.Context(), db.CreateChatMessageParams{
		ChatSessionID: sessionID,
		Role:          "assistant",
		Content:       content,
		TaskID:        taskID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create dm message")
		return
	}
	// Event-driven unread: stamps unread_since on the first unread assistant
	// message so the recipient's contact list / FAB badge lights up live.
	if err := h.Queries.SetUnreadSinceIfNull(r.Context(), sessionID); err != nil {
		slog.Warn("dm: set unread_since failed", "session", uuidToString(sessionID), "error", err)
	}
	if err := h.Queries.TouchChatSession(r.Context(), sessionID); err != nil {
		slog.Warn("dm: touch session failed", "session", uuidToString(sessionID), "error", err)
	}

	resolvedSessionID := uuidToString(sessionID)
	h.publishChat(protocol.EventChatMessage, workspaceID, "agent", actorID, resolvedSessionID, protocol.ChatMessagePayload{
		ChatSessionID: resolvedSessionID,
		MessageID:     uuidToString(msg.ID),
		Role:          "assistant",
		Content:       content,
		TaskID:        uuidToString(taskID),
		CreatedAt:     timestampToString(msg.CreatedAt),
	})

	writeJSON(w, http.StatusCreated, map[string]string{
		"chat_session_id": resolvedSessionID,
		"message_id":      uuidToString(msg.ID),
	})
}

// resolveDMRecipient picks the human a proactive DM should reach: the current
// task's initiator, else the agent's owner. The chosen id must be a real
// workspace member (human) — otherwise the DM is refused so agent<->agent talk
// is forced into channels.
func (h *Handler) resolveDMRecipient(r *http.Request, wsUUID, agentUUID pgtype.UUID) (pgtype.UUID, bool) {
	var candidate pgtype.UUID
	if tid := r.Header.Get("X-Task-ID"); tid != "" {
		if taskUUID, err := util.ParseUUID(tid); err == nil {
			_ = h.DB.QueryRow(r.Context(),
				`SELECT initiator_user_id FROM agent_task_queue WHERE id = $1`, taskUUID).Scan(&candidate)
		}
	}
	if !candidate.Valid {
		_ = h.DB.QueryRow(r.Context(),
			`SELECT owner_id FROM agent WHERE id = $1`, agentUUID).Scan(&candidate)
	}
	if !candidate.Valid {
		return pgtype.UUID{}, false
	}
	var isMember bool
	if err := h.DB.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM member WHERE workspace_id = $1 AND user_id = $2)`,
		wsUUID, candidate).Scan(&isMember); err != nil || !isMember {
		return pgtype.UUID{}, false
	}
	return candidate, true
}

// dmSessionTitle derives a short session title from the first line of a DM.
func dmSessionTitle(content string) string {
	line := content
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > 40 {
		line = string(r[:40])
	}
	return line
}
