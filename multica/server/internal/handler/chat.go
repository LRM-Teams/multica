package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/messageparts"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// chatSessionTitleMaxLen caps the rename input. Long enough to fit a
// meaningful summary, short enough to keep the dropdown row scannable.
const chatSessionTitleMaxLen = 200

// ---------------------------------------------------------------------------
// Chat Sessions
// ---------------------------------------------------------------------------

type CreateChatSessionRequest struct {
	AgentID string `json:"agent_id"`
	Title   string `json:"title"`
}

func (h *Handler) CreateChatSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())

	var req CreateChatSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	// Verify agent exists in workspace.
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusBadRequest, "agent is archived")
		return
	}
	session, err := h.Queries.CreateChatSession(r.Context(), db.CreateChatSessionParams{
		WorkspaceID: workspaceUUID,
		AgentID:     agentID,
		CreatorID:   parseUUID(userID),
		Title:       req.Title,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create chat session")
		return
	}

	writeJSON(w, http.StatusCreated, chatSessionToResponse(session))
}

func (h *Handler) ListChatSessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())

	// Compute the accessible-agents set once and use it to drop sessions
	// whose target agent the caller no longer has access to — without this,
	// a member whose role was downgraded would still see the session list
	// (and transcripts via ListChatMessages) for any private agent they
	// previously had access to. Falls back to the user's role from the
	// workspace member context.
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	allowed, ok := h.accessibleAgentIDs(r.Context(), workspaceID, actorType, actorID, member.Role)
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent access")
		return
	}

	status := r.URL.Query().Get("status")

	// Exclude channel-backed sessions: a channel agent session reuses a
	// chat_session (creator = the channel initiator, title "#channel"), so
	// without this filter every channel the user is in would surface as a fake
	// 1:1 DM. The chat panel is for genuine human↔agent DMs only.
	channelBound, err := h.channelBoundChatSessionIDs(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve chat sessions")
		return
	}

	// Two call sites → two row types with identical shape. Collect into a
	// common response slice via small per-branch loops.
	var resp []ChatSessionResponse
	if status == "all" {
		rows, err := h.Queries.ListAllChatSessionsByCreator(r.Context(), db.ListAllChatSessionsByCreatorParams{
			WorkspaceID: parseUUID(workspaceID),
			CreatorID:   parseUUID(userID),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list chat sessions")
			return
		}
		resp = make([]ChatSessionResponse, 0, len(rows))
		for _, s := range rows {
			if _, ok := allowed[uuidToString(s.AgentID)]; !ok {
				continue
			}
			if channelBound[uuidToString(s.ID)] {
				continue
			}
			resp = append(resp, ChatSessionResponse{
				ID:          uuidToString(s.ID),
				WorkspaceID: uuidToString(s.WorkspaceID),
				AgentID:     uuidToString(s.AgentID),
				CreatorID:   uuidToString(s.CreatorID),
				Title:       s.Title,
				Status:      s.Status,
				HasUnread:   s.HasUnread,
				CreatedAt:   timestampToString(s.CreatedAt),
				UpdatedAt:   timestampToString(s.UpdatedAt),
			})
		}
	} else {
		rows, err := h.Queries.ListChatSessionsByCreator(r.Context(), db.ListChatSessionsByCreatorParams{
			WorkspaceID: parseUUID(workspaceID),
			CreatorID:   parseUUID(userID),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list chat sessions")
			return
		}
		resp = make([]ChatSessionResponse, 0, len(rows))
		for _, s := range rows {
			if _, ok := allowed[uuidToString(s.AgentID)]; !ok {
				continue
			}
			if channelBound[uuidToString(s.ID)] {
				continue
			}
			resp = append(resp, ChatSessionResponse{
				ID:          uuidToString(s.ID),
				WorkspaceID: uuidToString(s.WorkspaceID),
				AgentID:     uuidToString(s.AgentID),
				CreatorID:   uuidToString(s.CreatorID),
				Title:       s.Title,
				Status:      s.Status,
				HasUnread:   s.HasUnread,
				CreatedAt:   timestampToString(s.CreatedAt),
				UpdatedAt:   timestampToString(s.UpdatedAt),
			})
		}
	}
	h.fillChatSessionProjects(r.Context(), resp)
	h.fillChatSessionRuntimeStats(r.Context(), resp)
	writeJSON(w, http.StatusOK, resp)
}

// channelBoundChatSessionIDs returns the set of chat_session ids in a workspace
// that back a channel agent session (or look like one). These are NOT 1:1 /
// bubble DMs and must be hidden from ListChatSessions and the agent bubble
// history dropdown.
//
// Sources:
//  1. Live channel_agent_session bindings (canonical).
//  2. Orphan shells whose title is still "#channelName" after the binding
//     row was deleted.
//  3. Any remaining "#token" title (no whitespace) — channel shells always
//     use that shape; catches renamed/deleted channels that (2) misses.
func (h *Handler) channelBoundChatSessionIDs(ctx context.Context, workspaceID string) (map[string]bool, error) {
	wsUUID := parseUUID(workspaceID)
	out := map[string]bool{}

	rows, err := h.DB.Query(ctx, `
		SELECT cas.chat_session_id
		FROM channel_agent_session cas
		JOIN channel ch ON ch.id = cas.channel_id
		WHERE ch.workspace_id = $1`, wsUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[uuidToString(id)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	orphanRows, err := h.DB.Query(ctx, `
		SELECT cs.id
		FROM chat_session cs
		WHERE cs.workspace_id = $1
		  AND (
		    EXISTS (
		      SELECT 1 FROM channel ch
		      WHERE ch.workspace_id = cs.workspace_id
		        AND cs.title = ('#' || ch.name)
		    )
		    OR cs.title ~ '^#[^[:space:]]+$'
		  )`, wsUUID)
	if err != nil {
		return nil, err
	}
	defer orphanRows.Close()
	for orphanRows.Next() {
		var id pgtype.UUID
		if err := orphanRows.Scan(&id); err != nil {
			return nil, err
		}
		out[uuidToString(id)] = true
	}
	return out, orphanRows.Err()
}

func (h *Handler) loadChatSessionForUser(w http.ResponseWriter, r *http.Request, userID, workspaceID, sessionID string) (db.ChatSession, bool) {
	sessionUUID, ok := parseUUIDOrBadRequest(w, sessionID, "chat session id")
	if !ok {
		return db.ChatSession{}, false
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.ChatSession{}, false
	}
	session, err := h.Queries.GetChatSessionInWorkspace(r.Context(), db.GetChatSessionInWorkspaceParams{
		ID:          sessionUUID,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "chat session not found")
		return db.ChatSession{}, false
	}
	if uuidToString(session.CreatorID) != userID {
		writeError(w, http.StatusForbidden, "not your chat session")
		return db.ChatSession{}, false
	}
	return session, true
}

// gateChatSessionForUser combines the session ownership check with the
// private-agent access gate so a member who has lost access to the target
// agent (role downgrade, ownership transfer, agent flipped to private)
// cannot continue reading the chat transcript even though they remain the
// session creator. Returns ok=false after writing the error response.
func (h *Handler) gateChatSessionForUser(w http.ResponseWriter, r *http.Request, userID, workspaceID, sessionID string) (db.ChatSession, bool) {
	session, ok := h.loadChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return db.ChatSession{}, false
	}
	if _, err := h.Queries.GetAgent(r.Context(), session.AgentID); err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return db.ChatSession{}, false
	}
	return session, true
}

func (h *Handler) GetChatSession(w http.ResponseWriter, r *http.Request) {
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

	resp := chatSessionToResponse(session)
	resp.ProjectID = h.chatSessionProjectID(r.Context(), session.ID)
	responses := []ChatSessionResponse{resp}
	h.fillChatSessionRuntimeStats(r.Context(), responses)
	writeJSON(w, http.StatusOK, responses[0])
}

type UpdateChatSessionRequest struct {
	Title *string `json:"title"`
	// Status soft-archives ("archived") or restores ("active") a session.
	// Archived sessions are read-only for SendChatMessage until restored.
	Status *string `json:"status"`
	// ProjectID binds (or clears) the chat's "current project". json.RawMessage
	// so we can tell absent (nil — leave unchanged) from null/"" (clear) from a
	// uuid (set). chat_session.project_id is a raw column the generated structs
	// don't carry, so it's read/written via raw SQL here.
	ProjectID json.RawMessage `json:"project_id"`
}

// UpdateChatSession updates user-editable fields on a chat session: `title`
// (inline rename), `status` (soft-archive / restore), and `project_id`
// (the composer "current project" that scopes the agent's working directory).
// agent/creator/workspace are immutable; the resume pointers
// (session_id / work_dir / runtime_id) are daemon-owned.
func (h *Handler) UpdateChatSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	sessionID := chi.URLParam(r, "sessionId")

	var req UpdateChatSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == nil && req.ProjectID == nil && req.Status == nil {
		writeError(w, http.StatusBadRequest, "no updatable fields")
		return
	}

	session, ok := h.gateChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return
	}

	updated := session
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		if len([]rune(title)) > chatSessionTitleMaxLen {
			writeError(w, http.StatusBadRequest, "title is too long")
			return
		}
		var err error
		updated, err = h.Queries.UpdateChatSessionTitle(r.Context(), db.UpdateChatSessionTitleParams{
			ID:    session.ID,
			Title: title,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update chat session")
			return
		}
	}

	if req.Status != nil {
		status := strings.TrimSpace(*req.Status)
		if status != "active" && status != "archived" {
			writeError(w, http.StatusBadRequest, "status must be active or archived")
			return
		}
		var err error
		updated, err = h.Queries.UpdateChatSessionStatus(r.Context(), db.UpdateChatSessionStatusParams{
			ID:     session.ID,
			Status: status,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update chat session")
			return
		}
	}

	if req.ProjectID != nil {
		var projectID pgtype.UUID // zero/invalid → clears the binding (NULL)
		if string(req.ProjectID) != "null" {
			var raw string
			if err := json.Unmarshal(req.ProjectID, &raw); err != nil {
				writeError(w, http.StatusBadRequest, "invalid project_id")
				return
			}
			if raw = strings.TrimSpace(raw); raw != "" {
				pid, perr := util.ParseUUID(raw)
				if perr != nil {
					writeError(w, http.StatusBadRequest, "invalid project_id")
					return
				}
				// The project must live in this workspace.
				proj, gerr := h.Queries.GetProject(r.Context(), pid)
				if gerr != nil || uuidToString(proj.WorkspaceID) != workspaceID {
					writeError(w, http.StatusBadRequest, "project not found")
					return
				}
				projectID = pid
			}
		}
		if _, err := h.DB.Exec(r.Context(),
			`UPDATE chat_session SET project_id = $2, updated_at = now() WHERE id = $1`,
			session.ID, projectID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update chat session")
			return
		}
	}

	resolvedSessionID := uuidToString(updated.ID)
	h.publishChatToCreator(protocol.EventChatSessionUpdated, workspaceID, "member", userID, resolvedSessionID, uuidToString(session.CreatorID), protocol.ChatSessionUpdatedPayload{
		ChatSessionID: resolvedSessionID,
		Title:         updated.Title,
		UpdatedAt:     timestampToString(updated.UpdatedAt),
	})

	resp := chatSessionToResponse(updated)
	resp.ProjectID = h.chatSessionProjectID(r.Context(), session.ID)
	writeJSON(w, http.StatusOK, resp)
}

// DeleteChatSession hard-deletes a chat session owned by the caller. The
// row lock + cancel + delete run inside a single tx so a concurrent
// SendChatMessage cannot enqueue a task that would later be orphaned by
// the FK ON DELETE SET NULL on agent_inbox_event.chat_session_id. Cancel
// failure aborts the delete; events fire only after commit.
func (h *Handler) DeleteChatSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	sessionID := chi.URLParam(r, "sessionId")

	session, ok := h.loadChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// FOR UPDATE on the chat_session row blocks any concurrent INSERT into
	// agent_inbox_event that references it (the FK validation needs a
	// KEY SHARE lock). After we commit the delete, the blocked INSERT
	// fails its FK check, so it can't land an orphaned task.
	if _, err := qtx.LockChatSessionForDelete(r.Context(), session.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Already gone — treat as idempotent success.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to lock chat session")
		return
	}

	cancelled, err := qtx.CancelAgentTasksByChatSession(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel chat session tasks")
		return
	}

	if err := qtx.DeleteChatSession(r.Context(), db.DeleteChatSessionParams{
		ID:          session.ID,
		WorkspaceID: session.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete chat session")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Warn("commit chat session delete failed", "session_id", sessionID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to commit chat session delete")
		return
	}

	// Post-commit broadcasts. Subscribers should never observe events for a
	// tx that didn't actually persist.
	h.TaskService.BroadcastCancelledTasks(r.Context(), cancelled)

	resolvedSessionID := uuidToString(session.ID)
	h.publishChatToCreator(protocol.EventChatSessionDeleted, workspaceID, "member", userID, resolvedSessionID, uuidToString(session.CreatorID), protocol.ChatSessionDeletedPayload{
		ChatSessionID: resolvedSessionID,
	})

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Chat Messages
// ---------------------------------------------------------------------------

type SendChatMessageRequest struct {
	Content       string                 `json:"content"`
	Parts         []protocol.MessagePart `json:"parts"`
	AttachmentIDs []string               `json:"attachment_ids"`
}

type SendChatMessageResponse struct {
	MessageID string `json:"message_id"`
	TaskID    string `json:"task_id"`
	// CreatedAt anchors the chat StatusPill timer the instant the user
	// hits send. Without it the front-end falls back to its local clock
	// and the timer "snaps backwards" later when WS events deliver the
	// real created_at. Returning it here means the pill renders 0s from
	// the start with a stable anchor.
	CreatedAt string `json:"created_at"`
}

func (h *Handler) SendChatMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	sessionID := chi.URLParam(r, "sessionId")

	var req SendChatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	content, parts, err := messageparts.Normalize(req.Content, req.Parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	// Pre-validate attachment ids early so invalid input returns 400 before
	// any state mutation. The actual link runs after CreateChatMessage so we
	// have a message_id to back-fill into the attachment rows.
	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, req.AttachmentIDs, "attachment_ids")
	if !ok {
		return
	}

	// Load chat session and re-check the private-agent gate on every send.
	// The session's creator passed the gate at create time, but their
	// workspace role (or the agent's owner) may have changed since — keep
	// stale sessions from being a back-door into a private agent the user
	// can no longer reach. Agent senders bypass to preserve A2A collaboration.
	session, ok := h.gateChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return
	}
	// New archive flow doesn't exist anymore, but legacy rows with
	// status='archived' may still be in the DB from before the feature
	// was removed. Refuse to enqueue new agent work for them — frontend
	// surfaces these as read-only.
	if session.Status != "active" {
		writeError(w, http.StatusBadRequest, "chat session is archived")
		return
	}

	// Create the user message first so the daemon can always find it.
	msg, err := h.Queries.CreateChatMessage(r.Context(), db.CreateChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "user",
		Content:       content,
		Parts:         messageparts.MustJSON(parts),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create chat message")
		return
	}

	// Back-fill chat_message_id on attachments that were uploaded against
	// this session while the user was composing. The query only touches rows
	// where chat_session_id matches AND chat_message_id IS NULL, so it cannot
	// rebind an attachment that already belongs to an earlier message.
	if len(attachmentIDs) > 0 {
		if err := h.Queries.LinkAttachmentsToChatMessage(r.Context(), db.LinkAttachmentsToChatMessageParams{
			ChatMessageID: msg.ID,
			ChatSessionID: session.ID,
			Column3:       attachmentIDs,
		}); err != nil {
			// Don't fail the send — the message content is already saved and
			// the attachments remain on the session (still downloadable).
			slog.Warn("link chat attachments failed", "error", err, "message_id", uuidToString(msg.ID))
		}
	}

	resolvedSessionID := uuidToString(session.ID)

	// L4 greeting fast path: standalone chat_session + pure hi/你好 → reply
	// with a builtin sticker immediately. Skip agent enqueue so greetings
	// never spiral into CLI diagnostics.
	if len(attachmentIDs) == 0 &&
		isPureStandaloneChatGreeting(content) &&
		h.channelIDForChatSession(r.Context(), session.ID) == "" {
		stickerContent, stickerParts, normErr := messageparts.Normalize("", []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: "hi",
			PackID:    messageparts.BuiltinStickerPackID,
		}})
		if normErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to build greeting sticker: "+normErr.Error())
			return
		}
		assistant, aerr := h.Queries.CreateChatMessage(r.Context(), db.CreateChatMessageParams{
			ChatSessionID: session.ID,
			Role:          "assistant",
			Content:       stickerContent,
			Parts:         messageparts.MustJSON(stickerParts),
			ElapsedMs:     pgtype.Int8{Int64: 0, Valid: true},
		})
		if aerr != nil {
			writeError(w, http.StatusInternalServerError, "failed to create greeting reply")
			return
		}
		if err := h.Queries.SetUnreadSinceIfNull(r.Context(), session.ID); err != nil {
			slog.Warn("failed to set unread_since on greeting fast path", "session_id", sessionID, "error", err)
		}
		if err := h.Queries.TouchChatSession(r.Context(), session.ID); err != nil {
			slog.Warn("failed to touch chat session", "session_id", sessionID, "error", err)
		}
		h.clearDMPeerHiddenForChatSession(r.Context(), workspaceID, userID, session.AgentID)

		h.publishChatToCreator(protocol.EventChatMessage, workspaceID, "member", userID, resolvedSessionID, uuidToString(session.CreatorID), protocol.ChatMessagePayload{
			ChatSessionID: resolvedSessionID,
			MessageID:     uuidToString(msg.ID),
			Role:          "user",
			Content:       content,
			Parts:         parts,
			CreatedAt:     timestampToString(msg.CreatedAt),
		})
		h.publishChatToCreator(protocol.EventChatDone, workspaceID, "system", "", resolvedSessionID, uuidToString(session.CreatorID), protocol.ChatDonePayload{
			ChatSessionID: resolvedSessionID,
			Type:          protocol.ChatOutputKindMessage,
			MessageID:     uuidToString(assistant.ID),
			Content:       assistant.Content,
			Parts:         stickerParts,
			CreatedAt:     timestampToString(assistant.CreatedAt),
			ElapsedMs:     0,
		})

		writeJSON(w, http.StatusCreated, SendChatMessageResponse{
			MessageID: uuidToString(msg.ID),
			TaskID:    "",
			CreatedAt: timestampToString(msg.CreatedAt),
		})
		return
	}

	// Enqueue a chat task after the message exists. For web chat the sender is
	// the authenticated request user (sessions are creator-only), so they are
	// the task initiator — surfaced to the agent under `## Task Initiator`.
	task, err := h.TaskService.EnqueueChatTask(r.Context(), session, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue chat task: "+err.Error())
		return
	}
	if err := h.Queries.LinkChatMessageToTask(r.Context(), db.LinkChatMessageToTaskParams{
		ID:     msg.ID,
		TaskID: task.ID,
	}); err != nil {
		// Don't fail the send: the task already exists and the user message
		// is persisted. The link is only needed for precise empty-cancel
		// cleanup; older/unlinked rows simply keep the historical behavior.
		slog.Warn("link user chat message to task failed",
			"message_id", uuidToString(msg.ID),
			"task_id", uuidToString(task.ID),
			"error", err,
		)
	}

	// Touch session updated_at.
	if err := h.Queries.TouchChatSession(r.Context(), session.ID); err != nil {
		slog.Warn("failed to touch chat session", "session_id", sessionID, "error", err)
	}
	h.clearDMPeerHiddenForChatSession(r.Context(), workspaceID, userID, session.AgentID)
	taskContext := h.TaskService.AnalyticsContextForTask(r.Context(), task)
	platform, _, _ := middleware.ClientMetadataFromContext(r.Context())
	obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.ChatMessageSent(
		userID,
		workspaceID,
		uuidToString(session.ID),
		uuidToString(task.ID),
		uuidToString(session.AgentID),
		taskContext.RuntimeMode,
		taskContext.Provider,
		platform,
	))

	// Broadcast the user message.
	h.publishChatToCreator(protocol.EventChatMessage, workspaceID, "member", userID, resolvedSessionID, uuidToString(session.CreatorID), protocol.ChatMessagePayload{
		ChatSessionID: resolvedSessionID,
		MessageID:     uuidToString(msg.ID),
		Role:          "user",
		Content:       content,
		Parts:         parts,
		TaskID:        uuidToString(task.ID),
		CreatedAt:     timestampToString(msg.CreatedAt),
	})

	writeJSON(w, http.StatusCreated, SendChatMessageResponse{
		MessageID: uuidToString(msg.ID),
		TaskID:    uuidToString(task.ID),
		CreatedAt: timestampToString(task.CreatedAt),
	})
}

type ChatMessagesCursorResponse struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type ChatMessagesPageResponse struct {
	Messages   []ChatMessageResponse       `json:"messages"`
	Limit      int                         `json:"limit"`
	HasMore    bool                        `json:"has_more"`
	NextCursor *ChatMessagesCursorResponse `json:"next_cursor,omitempty"`
}

func parseChatMessagesPageParams(r *http.Request) (int, pgtype.Timestamptz, pgtype.UUID, error) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("invalid limit")
		}
		limit = parsed
	}

	rawBeforeCreatedAt := r.URL.Query().Get("before_created_at")
	rawBeforeID := r.URL.Query().Get("before_id")
	if rawBeforeCreatedAt == "" && rawBeforeID == "" {
		return limit, pgtype.Timestamptz{}, pgtype.UUID{}, nil
	}
	if rawBeforeCreatedAt == "" || rawBeforeID == "" {
		return 0, pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("invalid cursor")
	}
	beforeTime, err := time.Parse(time.RFC3339Nano, rawBeforeCreatedAt)
	if err != nil {
		return 0, pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("invalid cursor")
	}
	beforeID, err := util.ParseUUID(rawBeforeID)
	if err != nil {
		return 0, pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("invalid cursor")
	}
	return limit, pgtype.Timestamptz{Time: beforeTime, Valid: true}, beforeID, nil
}

func (h *Handler) ListChatMessages(w http.ResponseWriter, r *http.Request) {
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

	messages, err := h.Queries.ListChatMessages(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list chat messages")
		return
	}

	messageIDs := make([]pgtype.UUID, len(messages))
	for i, m := range messages {
		messageIDs[i] = m.ID
	}
	groupedAtt := h.groupChatMessageAttachments(r.Context(), workspaceID, messageIDs)

	resp := make([]ChatMessageResponse, len(messages))
	for i, m := range messages {
		resp[i] = chatMessageToResponse(m, groupedAtt[uuidToString(m.ID)])
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListChatMessagesPage(w http.ResponseWriter, r *http.Request) {
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

	limit, beforeCreatedAt, beforeID, err := parseChatMessagesPageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	messages, err := h.Queries.ListChatMessagesPage(r.Context(), db.ListChatMessagesPageParams{
		ChatSessionID:   session.ID,
		Limit:           int32(limit + 1),
		BeforeCreatedAt: beforeCreatedAt,
		BeforeID:        beforeID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list chat messages")
		return
	}
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	var nextCursor *ChatMessagesCursorResponse
	if hasMore && len(messages) > 0 {
		oldest := messages[len(messages)-1]
		nextCursor = &ChatMessagesCursorResponse{
			CreatedAt: oldest.CreatedAt.Time.Format(time.RFC3339Nano),
			ID:        uuidToString(oldest.ID),
		}
	}
	// SQL fetches newest windows first so the empty cursor opens at the recent
	// tail. Reverse each cursor page before serializing to keep message order
	// chronological within the viewport.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	messageIDs := make([]pgtype.UUID, len(messages))
	for i, m := range messages {
		messageIDs[i] = m.ID
	}
	groupedAtt := h.groupChatMessageAttachments(r.Context(), workspaceID, messageIDs)

	resp := make([]ChatMessageResponse, len(messages))
	for i, m := range messages {
		resp[i] = chatMessageToResponse(m, groupedAtt[uuidToString(m.ID)])
	}
	writeJSON(w, http.StatusOK, ChatMessagesPageResponse{
		Messages:   resp,
		Limit:      limit,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

// PendingChatTaskResponse is returned by GetPendingChatTask — either the
// current in-flight task's id/status/inbox_event_id, or an empty object when
// none is active. CreatedAt is the anchor the frontend uses to time the chat
// StatusPill (elapsed seconds = now - CreatedAt). It must come from the server
// because optimistic seeds don't have a real task created_at and the timer
// needs to survive refresh / reopen.
type PendingChatTaskResponse struct {
	TaskID       string  `json:"task_id,omitempty"`
	Status       string  `json:"status,omitempty"`
	CreatedAt    string  `json:"created_at,omitempty"`
	InboxEventID *string `json:"inbox_event_id,omitempty"`
}

// MarkChatSessionRead clears the session's unread_since (→ has_unread=false)
// and broadcasts chat:session_read so other devices of the same user drop
// their badges.
func (h *Handler) MarkChatSessionRead(w http.ResponseWriter, r *http.Request) {
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

	if err := h.Queries.MarkChatSessionRead(r.Context(), session.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark session read")
		return
	}
	h.clearDMPeerManualUnread(r.Context(), workspaceID, userID, dmPeerRef{Type: "agent", ID: session.AgentID})

	resolvedSessionID := uuidToString(session.ID)
	h.publishChatToCreator(protocol.EventChatSessionRead, workspaceID, "member", userID, resolvedSessionID, uuidToString(session.CreatorID), protocol.ChatSessionReadPayload{
		ChatSessionID: resolvedSessionID,
	})

	w.WriteHeader(http.StatusNoContent)
}

// PendingChatTasksResponse is the aggregate view consumed by the FAB.
type PendingChatTasksResponse struct {
	Tasks []PendingChatTaskItem `json:"tasks"`
}

type PendingChatTaskItem struct {
	TaskID        string `json:"task_id"`
	Status        string `json:"status"`
	ChatSessionID string `json:"chat_session_id"`
}

type CancelledChatMessageResponse struct {
	ChatSessionID  string `json:"chat_session_id"`
	MessageID      string `json:"message_id"`
	Content        string `json:"content"`
	RestoreToInput bool   `json:"restore_to_input"`
}

type CancelTaskByUserResponse struct {
	AgentTaskResponse
	CancelledChatMessage *CancelledChatMessageResponse `json:"cancelled_chat_message,omitempty"`
}

// ListPendingChatTasks returns every in-flight chat task owned by the current
// user in this workspace. Drives the FAB's "running" indicator when the chat
// window is closed (no per-session query is subscribed). Tasks belonging to
// private agents the caller has lost access to are dropped from the response.
func (h *Handler) ListPendingChatTasks(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())

	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	allowed, ok := h.accessibleAgentIDs(r.Context(), workspaceID, actorType, actorID, member.Role)
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent access")
		return
	}

	rows, err := h.Queries.ListPendingChatTasksByCreator(r.Context(), db.ListPendingChatTasksByCreatorParams{
		WorkspaceID: parseUUID(workspaceID),
		CreatorID:   parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pending chat tasks")
		return
	}

	// Map session → agent so we can filter without an N+1. The user's own
	// session list is small, so one extra query is cheaper than per-row
	// lookups.
	sessions, err := h.Queries.ListAllChatSessionsByCreator(r.Context(), db.ListAllChatSessionsByCreatorParams{
		WorkspaceID: parseUUID(workspaceID),
		CreatorID:   parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve chat session agents")
		return
	}
	sessionAgent := make(map[string]string, len(sessions))
	for _, s := range sessions {
		sessionAgent[uuidToString(s.ID)] = uuidToString(s.AgentID)
	}

	items := make([]PendingChatTaskItem, 0, len(rows))
	for _, row := range rows {
		sessionID := uuidToString(row.ChatSessionID)
		agentID, hasAgent := sessionAgent[sessionID]
		if !hasAgent {
			continue
		}
		if _, ok := allowed[agentID]; !ok {
			continue
		}
		items = append(items, PendingChatTaskItem{
			TaskID:        uuidToString(row.TaskID),
			Status:        row.Status,
			ChatSessionID: sessionID,
		})
	}
	writeJSON(w, http.StatusOK, PendingChatTasksResponse{Tasks: items})
}

// GetPendingChatTask returns the most recent in-flight task (queued / dispatched
// / running) for a chat session. The frontend polls this on mount / session
// switch so pending UI state survives refresh and reopen.
func (h *Handler) GetPendingChatTask(w http.ResponseWriter, r *http.Request) {
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

	task, err := h.Queries.GetPendingChatTask(r.Context(), session.ID)
	if err != nil {
		// No in-flight task — return an empty object, not an error.
		writeJSON(w, http.StatusOK, PendingChatTaskResponse{})
		return
	}

	writeJSON(w, http.StatusOK, PendingChatTaskResponse{
		TaskID:       uuidToString(task.ID),
		Status:       task.Status,
		CreatedAt:    timestampToString(task.CreatedAt),
		InboxEventID: uuidStringPtr(task.InboxEventID),
	})
}

func (h *Handler) CancelChatAgentInboxEvent(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "sessionId")
	session, ok := h.gateChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return
	}
	eventID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "inbox event id")
	if !ok {
		return
	}

	var eventAgentID, eventStatus, terminalOutcome string
	if err := h.DB.QueryRow(r.Context(), `
		SELECT e.agent_id, e.status, COALESCE(e.terminal_outcome, '')
		FROM agent_inbox_event e
		WHERE e.id = $1
		  AND e.workspace_id = $2
		  AND e.chat_session_id = $3
		  AND e.requires_wake
	`, eventID, workspaceUUID, session.ID).Scan(&eventAgentID, &eventStatus, &terminalOutcome); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "inbox event not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load inbox event")
		return
	}

	if terminalOutcome != "" || eventStatus == "acked" || eventStatus == "suppressed" {
		writeError(w, http.StatusBadRequest, "task is not cancellable")
		return
	}

	row, err := h.cancelAgentInboxEventCore(r.Context(), workspaceUUID, eventID)
	if err != nil {
		if errors.Is(err, errAgentInboxEventNotCancellable) {
			writeError(w, http.StatusBadRequest, "task is not cancellable")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to cancel chat inbox event")
		return
	}

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	payload := h.cancelledInboxEventTaskResponse(row, workspaceID)
	h.publishCancelledAgentInboxEvent(workspaceID, actorType, actorID, row, payload)
	writeJSON(w, http.StatusOK, ChannelCancelAgentInboxEventResponse{
		OK:           true,
		InboxEventID: uuidToString(row.ID),
		AgentID:      eventAgentID,
		Status:       "cancelled",
	})
}

func (h *Handler) ListChatAgentInboxEventTimeline(w http.ResponseWriter, r *http.Request) {
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

	eventID := chi.URLParam(r, "eventId")
	eventUUID, ok := parseUUIDOrBadRequest(w, eventID, "inbox_event_id")
	if !ok {
		return
	}

	var exists bool
	if err := h.DB.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1
			FROM agent_inbox_event
			WHERE id = $1
			  AND workspace_id = $2
			  AND chat_session_id = $3
			  AND agent_id = $4
		)
	`, eventUUID, session.WorkspaceID, session.ID, session.AgentID).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve chat transcript")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "chat transcript not found")
		return
	}

	resp, err := h.projectInboxEventTaskMessages(r.Context(), eventUUID, eventID, session.WorkspaceID, r.URL.Query().Get("since"))
	if err != nil {
		if errors.Is(err, errInvalidTaskMessageSince) {
			writeError(w, http.StatusBadRequest, "invalid since parameter")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to list chat transcript")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Task cancellation (user-facing, with ownership check)
// ---------------------------------------------------------------------------

// CancelTaskByUser cancels a task the caller is allowed to act on within the
// current workspace.
//
// Tenancy is enforced uniformly through the task's owning agent: every
// agent_inbox_event row carries a NOT NULL agent_id (ON DELETE CASCADE, so the
// agent always exists), and agents are workspace-scoped. GetAgentTaskInWorkspace
// is therefore the single tenant guard that works regardless of which optional
// source FK (issue / chat_session / autopilot_run) is set — which is what makes
// run_only autopilot tasks and quick_create tasks (whose issue does not exist
// yet) cancellable at all. Keying cancellation off issue_id / chat_session_id
// alone is exactly what 404'd these tasks before (MUL-2827).
//
// On top of tenancy, two privacy models layer on:
//   - a chat task is private to the member who started the conversation, so
//     only that creator may cancel it;
//   - every other task surfaces on the agent Activity tab and the workspace
//     task snapshot, both of which hide private agents from members without
//     access. Cancellation mirrors that gate via canAccessPrivateAgent so the
//     id-only endpoint is never more permissive than the surface that exposes
//     the task.
func (h *Handler) CancelTaskByUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskId")
	taskUUID, ok := parseUUIDOrBadRequest(w, taskID, "task id")
	if !ok {
		return
	}

	task, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{
		ID:          taskUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		if h.cancelAgentInboxEventByUser(w, r, wsUUID, taskUUID, userID, workspaceID) {
			return
		}
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	if task.ChatSessionID.Valid {
		// Chat privacy: direct chats are creator-only, but channel-backed
		// agent runs are shared channel work and may be stopped by any human
		// member of that channel.
		channelID := h.channelIDForChatSession(r.Context(), task.ChatSessionID)
		if channelID != "" {
			if !h.requireChannelUserMember(w, r.Context(), workspaceID, parseUUID(channelID), parseUUID(userID)) {
				return
			}
		} else {
			cs, err := h.Queries.GetChatSessionInWorkspace(r.Context(), db.GetChatSessionInWorkspaceParams{
				ID:          task.ChatSessionID,
				WorkspaceID: wsUUID,
			})
			if err != nil {
				writeError(w, http.StatusNotFound, "task not found")
				return
			}
			if uuidToString(cs.CreatorID) != userID {
				writeError(w, http.StatusForbidden, "not your task")
				return
			}
		}
	} else {
		// Cancelling an issue / autopilot / quick_create task is a control
		// action on the agent (task #908 principle: control actions stay
		// admin|owner, same predicate as the Activity tab) — not a chat/DM
		// interaction, so it does not widen with the rest of #908.
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID:          task.AgentID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		actorType, actorID := h.resolveActor(r, userID, workspaceID)
		if !h.canAccessAgentInternals(r.Context(), agent, actorType, actorID, workspaceID) {
			writeError(w, http.StatusForbidden, "you do not have access to this agent")
			return
		}
	}

	cancelled, err := h.TaskService.CancelTaskWithResult(r.Context(), taskUUID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := CancelTaskByUserResponse{
		AgentTaskResponse: taskToResponse(cancelled.Task, workspaceID),
	}
	if cancelled.CancelledChatMessage != nil {
		resp.CancelledChatMessage = &CancelledChatMessageResponse{
			ChatSessionID:  cancelled.CancelledChatMessage.ChatSessionID,
			MessageID:      cancelled.CancelledChatMessage.MessageID,
			Content:        cancelled.CancelledChatMessage.Content,
			RestoreToInput: cancelled.CancelledChatMessage.RestoreToInput,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// channelAgentInboxCancelPathError tells clients that channel wake cancels must
// use the explicit inbox contract (LRM-425), not POST /api/tasks/{id}/cancel.
const channelAgentInboxCancelPathError = "channel agent inbox events must be cancelled via POST /api/channels/{channelId}/agent-inbox/events/{eventId}/cancel or POST /api/channels/{channelId}/agent-inbox/cancel-active"

func (h *Handler) cancelAgentInboxEventByUser(w http.ResponseWriter, r *http.Request, workspaceUUID, inboxEventID pgtype.UUID, userID, workspaceID string) bool {
	ctx := r.Context()
	var eventAgentID, channelID, chatSessionID pgtype.UUID
	var eventStatus, terminalOutcome string
	if err := h.DB.QueryRow(ctx, `
		SELECT e.agent_id,
		       e.channel_id,
		       e.chat_session_id,
		       e.status,
		       COALESCE(e.terminal_outcome, '')
		FROM agent_inbox_event e
		WHERE e.id = $1
		  AND e.workspace_id = $2
		  AND e.requires_wake`, inboxEventID, workspaceUUID).Scan(&eventAgentID, &channelID, &chatSessionID, &eventStatus, &terminalOutcome); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false
		}
		writeError(w, http.StatusInternalServerError, "failed to load agent inbox task")
		return true
	}

	// Channel-scoped wakes no longer cancel through the tasks-cancel dual path
	// (LRM-425 / LRM-238). Clients must call the channel agent-inbox cancel APIs.
	if channelID.Valid {
		writeError(w, http.StatusConflict, channelAgentInboxCancelPathError)
		return true
	}

	if chatSessionID.Valid {
		if sessionChannelID := h.channelIDForChatSession(ctx, chatSessionID); sessionChannelID != "" {
			writeError(w, http.StatusConflict, channelAgentInboxCancelPathError)
			return true
		}
		cs, err := h.Queries.GetChatSessionInWorkspace(ctx, db.GetChatSessionInWorkspaceParams{
			ID:          chatSessionID,
			WorkspaceID: workspaceUUID,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "task not found")
			return true
		}
		if uuidToString(cs.CreatorID) != userID {
			writeError(w, http.StatusForbidden, "not your task")
			return true
		}
	} else {
		// Cancelling an inbox event is a control action on the agent (task
		// #908 principle: control actions stay admin|owner). See the
		// matching cancel-task branch above.
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          eventAgentID,
			WorkspaceID: workspaceUUID,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "task not found")
			return true
		}
		actorType, actorID := h.resolveActor(r, userID, workspaceID)
		if !h.canAccessAgentInternals(ctx, agent, actorType, actorID, workspaceID) {
			writeError(w, http.StatusForbidden, "you do not have access to this agent")
			return true
		}
	}

	if terminalOutcome != "" || eventStatus == "acked" || eventStatus == "suppressed" {
		writeError(w, http.StatusBadRequest, "task is not cancellable")
		return true
	}

	row, err := h.cancelAgentInboxEventCore(ctx, workspaceUUID, inboxEventID)
	if err != nil {
		if errors.Is(err, errAgentInboxEventNotCancellable) {
			writeError(w, http.StatusBadRequest, "task is not cancellable")
			return true
		}
		writeError(w, http.StatusInternalServerError, "failed to cancel agent inbox task")
		return true
	}

	resp := CancelTaskByUserResponse{
		AgentTaskResponse: h.cancelledInboxEventTaskResponse(row, workspaceID),
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	h.publishCancelledAgentInboxEvent(workspaceID, actorType, actorID, row, resp.AgentTaskResponse)
	writeJSON(w, http.StatusOK, resp)
	return true
}

// ---------------------------------------------------------------------------
// Response types & helpers
// ---------------------------------------------------------------------------

type ChatSessionResponse struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	CreatorID   string `json:"creator_id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	// ProjectID is the chat's bound project (composer "current project"), or
	// empty. Filled separately so single and list endpoints stay consistent.
	ProjectID    string                      `json:"project_id,omitempty"`
	RuntimeStats *protocol.RuntimeTokenStats `json:"runtime_stats,omitempty"`
	// Only populated by list endpoints — single-session fetches return false.
	HasUnread bool   `json:"has_unread"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type ChatMessageResponse struct {
	ID            string                 `json:"id"`
	ChatSessionID string                 `json:"chat_session_id"`
	Role          string                 `json:"role"`
	Content       string                 `json:"content"`
	Parts         []protocol.MessagePart `json:"parts,omitempty"`
	TaskID        *string                `json:"task_id"`
	CreatedAt     string                 `json:"created_at"`
	// FailureReason flags an assistant row synthesized by FailTask's chat
	// fallback. Front-end uses it to switch to the destructive bubble.
	FailureReason *string `json:"failure_reason"`
	// ElapsedMs is the wall-clock duration from task creation to terminal
	// state. Drives "Replied in 38s" / "Failed after 12s" captions.
	ElapsedMs *int64 `json:"elapsed_ms"`
	// Attachments linked to this message via chat_message_id. The chat
	// bubble renders file cards from these, and the daemon claim path
	// (daemon.go) pulls structured metadata from the same source so the
	// agent can `multica attachment view --id <id> --output <path>` rather than guessing
	// from a markdown URL that may expire.
	Attachments []AttachmentResponse `json:"attachments,omitempty"`
}

func chatSessionToResponse(s db.ChatSession) ChatSessionResponse {
	return ChatSessionResponse{
		ID:          uuidToString(s.ID),
		WorkspaceID: uuidToString(s.WorkspaceID),
		AgentID:     uuidToString(s.AgentID),
		CreatorID:   uuidToString(s.CreatorID),
		Title:       s.Title,
		Status:      s.Status,
		CreatedAt:   timestampToString(s.CreatedAt),
		UpdatedAt:   timestampToString(s.UpdatedAt),
	}
}

// chatSessionProjectID reads a single chat session's bound project_id (a column
// the generated ChatSession struct doesn't carry). Empty when unbound or on
// error.
func (h *Handler) chatSessionProjectID(ctx context.Context, id pgtype.UUID) string {
	var pid pgtype.UUID
	if err := h.DB.QueryRow(ctx, `SELECT project_id FROM chat_session WHERE id = $1`, id).Scan(&pid); err == nil && pid.Valid {
		return uuidToString(pid)
	}
	return ""
}

// fillChatSessionProjects sets ProjectID on each response from a single batched
// read of chat_session.project_id. Best-effort: leaves ProjectID empty on error.
func (h *Handler) fillChatSessionRuntimeStats(ctx context.Context, resp []ChatSessionResponse) {
	if len(resp) == 0 {
		return
	}
	ids := make([]pgtype.UUID, 0, len(resp))
	for _, s := range resp {
		ids = append(ids, parseUUID(s.ID))
	}
	rows, err := h.DB.Query(ctx, `SELECT id, runtime_token_stats FROM chat_session WHERE id = ANY($1::uuid[]) AND runtime_token_stats IS NOT NULL`, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	byID := make(map[string]*protocol.RuntimeTokenStats, len(resp))
	for rows.Next() {
		var id pgtype.UUID
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil || len(raw) == 0 {
			continue
		}
		var stats protocol.RuntimeTokenStats
		if json.Unmarshal(raw, &stats) == nil {
			byID[uuidToString(id)] = &stats
		}
	}
	for i := range resp {
		if stats, ok := byID[resp[i].ID]; ok {
			resp[i].RuntimeStats = stats
		}
	}
}

func (h *Handler) fillChatSessionProjects(ctx context.Context, resp []ChatSessionResponse) {
	if len(resp) == 0 {
		return
	}
	ids := make([]pgtype.UUID, 0, len(resp))
	for _, s := range resp {
		ids = append(ids, parseUUID(s.ID))
	}
	rows, err := h.DB.Query(ctx, `SELECT id, project_id FROM chat_session WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	byID := make(map[string]string, len(resp))
	for rows.Next() {
		var id, pid pgtype.UUID
		if err := rows.Scan(&id, &pid); err != nil {
			continue
		}
		if pid.Valid {
			byID[uuidToString(id)] = uuidToString(pid)
		}
	}
	for i := range resp {
		if pid, ok := byID[resp[i].ID]; ok {
			resp[i].ProjectID = pid
		}
	}
}

func chatMessageToResponse(m db.ChatMessage, attachments []AttachmentResponse) ChatMessageResponse {
	content := m.Content
	parts := messageparts.Decode(m.Parts)
	if m.Role == "assistant" {
		if unwrappedContent, unwrappedParts, unwrapped, err := messageparts.UnwrapStructuredMessageSend(content, parts); err == nil && unwrapped {
			content = unwrappedContent
			parts = unwrappedParts
		}
	}
	return ChatMessageResponse{
		ID:            uuidToString(m.ID),
		ChatSessionID: uuidToString(m.ChatSessionID),
		Role:          m.Role,
		Content:       content,
		Parts:         parts,
		TaskID:        uuidToPtr(m.TaskID),
		CreatedAt:     timestampToString(m.CreatedAt),
		FailureReason: textToPtr(m.FailureReason),
		ElapsedMs:     int8ToPtr(m.ElapsedMs),
		Attachments:   attachments,
	}
}
