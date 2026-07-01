package handler

import (
	"context"
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
	To      string `json:"to"`
}

// AgentDirectMessage lets an agent proactively send a 1:1 direct message to a
// human workspace member. DMs are strictly human <-> agent: an agent that needs
// to reach ANOTHER agent must post in a channel and @-mention it, so this
// endpoint only resolves workspace members as recipients.
//
// Auth: agent-only. The caller must be a task-scoped agent actor (resolveActor
// returns "agent"); a human/member request is rejected with 403. Without an
// explicit recipient, the recipient is the current task's initiator, falling
// back to the agent's owner.
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

	recipient, ok := h.resolveDMRecipient(r, wsUUID, agentUUID, req.To)
	if !ok {
		if strings.TrimSpace(req.To) != "" {
			writeError(w, http.StatusNotFound, "recipient workspace member not found or not reachable")
			return
		}
		writeError(w, http.StatusBadRequest, "no human recipient for this DM — to reach another agent, post in a channel and @-mention it")
		return
	}

	// The server owns the salutation: address the actual recipient by username
	// and drop any greeting the agent wrote itself (agents routinely grab the
	// wrong name from the issue/channel). This guarantees the name always
	// matches who the DM is delivered to.
	content = applyDMSalutation(content, h.userName(r.Context(), recipient))

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

	// Deliberately NOT linking the task: a DM is a plain conversational message,
	// not a task-execution record. Linking task_id makes the chat UI render the
	// agent's task timeline ("N steps") in place of the message text.
	msg, err := h.Queries.CreateChatMessage(r.Context(), db.CreateChatMessageParams{
		ChatSessionID: sessionID,
		Role:          "assistant",
		Content:       content,
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
	h.clearDMPeerHiddenForChatSession(r.Context(), workspaceID, uuidToString(recipient), agentUUID)

	resolvedSessionID := uuidToString(sessionID)
	h.publishChat(protocol.EventChatMessage, workspaceID, "agent", actorID, resolvedSessionID, protocol.ChatMessagePayload{
		ChatSessionID: resolvedSessionID,
		MessageID:     uuidToString(msg.ID),
		Role:          "assistant",
		Content:       content,
		CreatedAt:     timestampToString(msg.CreatedAt),
	})

	writeJSON(w, http.StatusCreated, map[string]string{
		"chat_session_id": resolvedSessionID,
		"message_id":      uuidToString(msg.ID),
	})
}

// resolveDMRecipient picks the human a proactive DM should reach. An explicit
// recipient may be a workspace member id, user id, username, display name, or
// email. Without one, the current task's initiator wins, then the agent owner.
// The chosen id must be a real workspace member (human) — otherwise the DM is
// refused so agent<->agent talk is forced into channels.
func (h *Handler) resolveDMRecipient(r *http.Request, wsUUID, agentUUID pgtype.UUID, rawTo string) (pgtype.UUID, bool) {
	if to := strings.TrimSpace(rawTo); to != "" {
		return h.resolveExplicitDMRecipient(r.Context(), wsUUID, to)
	}

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
	if !candidate.Valid || !h.isWorkspaceUserMember(r.Context(), wsUUID, candidate) {
		return pgtype.UUID{}, false
	}
	return candidate, true
}

func (h *Handler) resolveExplicitDMRecipient(ctx context.Context, wsUUID pgtype.UUID, rawTo string) (pgtype.UUID, bool) {
	if u, err := util.ParseUUID(rawTo); err == nil {
		var userID pgtype.UUID
		err := h.DB.QueryRow(ctx, `
			SELECT user_id FROM member
			WHERE workspace_id = $1 AND (id = $2 OR user_id = $2)
			LIMIT 1`, wsUUID, u).Scan(&userID)
		if err == nil {
			return userID, true
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, false
		}
	}

	var userID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT m.user_id
		FROM member m
		JOIN "user" u ON u.id = m.user_id
		WHERE m.workspace_id = $1
		  AND (lower(u.name) = lower($2) OR lower(to_jsonb(u)->>'display_name') = lower($2) OR lower(u.email) = lower($2))
		ORDER BY m.created_at ASC
		LIMIT 1`, wsUUID, rawTo).Scan(&userID)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return userID, true
}

func (h *Handler) isWorkspaceUserMember(ctx context.Context, wsUUID, userID pgtype.UUID) bool {
	var isMember bool
	err := h.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM member WHERE workspace_id = $1 AND user_id = $2)`,
		wsUUID, userID).Scan(&isMember)
	return err == nil && isMember
}

// userName returns a user's display name, or "" if unknown.
func (h *Handler) userName(ctx context.Context, id pgtype.UUID) string {
	var name string
	_ = h.DB.QueryRow(ctx, `SELECT COALESCE(NULLIF(to_jsonb(u)->>'display_name', ''), NULLIF(u.name, ''), u.email, '') FROM "user" u WHERE id = $1`, id).Scan(&name)
	return name
}

// applyDMSalutation strips any greeting the agent wrote (which often addresses
// the wrong person) and prepends the real recipient's username, so the name in
// the DM always matches who it's delivered to.
func applyDMSalutation(content, recipientName string) string {
	recipientName = strings.TrimSpace(recipientName)
	if recipientName == "" {
		return content
	}
	return recipientName + "，" + stripLeadingSalutation(content)
}

// stripLeadingSalutation removes a leading "<name> 你好，"-style greeting (the
// "你好"/"您好" must appear within the first 24 runes to count as a salutation,
// not as body text).
func stripLeadingSalutation(s string) string {
	s = strings.TrimLeft(s, " \t\r\n")
	r := []rune(s)
	scan := len(r)
	if scan > 24 {
		scan = 24
	}
	for i := 0; i+1 < scan; i++ {
		if (r[i] == '你' || r[i] == '您') && r[i+1] == '好' {
			// Only a salutation when nothing sentence-like precedes the greeting
			// (it's at the start, or after a short bare name) — otherwise a "你好"
			// inside the body would be wrongly chopped.
			if !salutationPrefixOK(r[:i]) {
				return s
			}
			j := i + 2
			for j < len(r) && isSalutationTail(r[j]) {
				j++
			}
			return strings.TrimLeft(string(r[j:]), " \t\r\n")
		}
	}
	return s
}

// salutationPrefixOK reports whether the runes before a greeting look like a
// bare name (or nothing) rather than body text.
func salutationPrefixOK(prefix []rune) bool {
	if len(prefix) > 16 {
		return false
	}
	for _, c := range prefix {
		switch c {
		case '，', '。', '！', '？', '、', '；', '：', ',', '.', '!', '?', ';', ':', '\n':
			return false
		}
	}
	return true
}

func isSalutationTail(c rune) bool {
	switch c {
	case '，', ',', '。', '!', '！', '~', '～', '、', '：', ':', ' ', '\t', '\r', '\n':
		return true
	}
	return false
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
