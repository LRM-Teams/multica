package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const channelNameMaxLen = 80
const channelMessageMaxLen = 20000
const channelContextMessageLimit = 30
const channelRunTriggerLimit = 10
const channelUserTypingExpiresInMS = 5000
const channelAgentTypingExpiresInMS = 10 * 60 * 1000

type ChannelResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Description *string `json:"description"`
	LarkChatID  *string `json:"lark_chat_id"`
	CreatedBy   string  `json:"created_by"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	// List-only enrichments (zero/omitted on create/update/get responses).
	UnreadCount int                  `json:"unread_count"`
	LastMessage *ChannelLastMessage  `json:"last_message,omitempty"`
	Members     []ChannelMemberBrief `json:"members,omitempty"`
}

type ChannelLastMessage struct {
	AuthorType string `json:"author_type"`
	AuthorName string `json:"author_name"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
}

type ChannelMemberBrief struct {
	MemberType string `json:"member_type"`
	MemberID   string `json:"member_id"`
	Name       string `json:"name"`
}

type ChannelMemberResponse struct {
	MemberType string `json:"member_type"`
	MemberID   string `json:"member_id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"created_at"`
}

type ChannelMessageResponse struct {
	ID                string  `json:"id"`
	ChannelID         string  `json:"channel_id"`
	WorkspaceID       string  `json:"workspace_id"`
	AuthorType        string  `json:"author_type"`
	AuthorID          *string `json:"author_id"`
	AuthorName        string  `json:"author_name"`
	Content           string  `json:"content"`
	Source            string  `json:"source"`
	ExternalMessageID *string `json:"external_message_id"`
	ThreadID          *string `json:"thread_id,omitempty"`
	TriggerDepth      int     `json:"trigger_depth"`
	CreatedAt         string  `json:"created_at"`
	// Attachments linked to this message via channel_message_id. The chat
	// bubble renders file/image cards from these.
	Attachments []AttachmentResponse `json:"attachments,omitempty"`
}

type CreateChannelRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	LarkChatID  *string `json:"lark_chat_id"`
}

type UpdateChannelRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	LarkChatID  *string `json:"lark_chat_id"`
}

type AddChannelMemberRequest struct {
	MemberType string `json:"member_type"`
	MemberID   string `json:"member_id"`
}

type SendChannelMessageRequest struct {
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
}

type ChannelTypingRequest struct {
	IsTyping bool `json:"is_typing"`
}

type ImportLarkChannelMessageRequest struct {
	LarkChatID        string `json:"lark_chat_id"`
	ExternalMessageID string `json:"external_message_id"`
	AuthorName        string `json:"author_name"`
	Content           string `json:"content"`
}

func (h *Handler) StartChannelBridge() {
	if h.Bus == nil {
		return
	}
	h.Bus.Subscribe(protocol.EventChatDone, h.handleChannelChatDone)
	h.Bus.Subscribe(protocol.EventTaskFailed, h.handleChannelChatStopped)
	h.Bus.Subscribe(protocol.EventTaskCancelled, h.handleChannelChatStopped)
}

func (h *Handler) ListChannels(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	uid := parseUUID(userID)
	rows, err := h.DB.Query(r.Context(), `
		SELECT ch.id, ch.workspace_id, ch.name, ch.description, ch.lark_chat_id, ch.created_by, ch.created_at, ch.updated_at, ch.kind,
		       lm.author_type, lm.author_name, lm.content, lm.created_at,
		       COALESCE(uc.cnt, 0)
		FROM channel ch
		JOIN channel_member cm ON cm.channel_id = ch.id AND cm.member_type = 'user' AND cm.member_id = $2
		LEFT JOIN LATERAL (
			SELECT author_type, author_name, content, created_at
			FROM channel_message m WHERE m.channel_id = ch.id
			ORDER BY m.created_at DESC LIMIT 1
		) lm ON true
		LEFT JOIN channel_read cr ON cr.channel_id = ch.id AND cr.user_id = $2
		LEFT JOIN LATERAL (
			SELECT count(*) AS cnt FROM channel_message m
			WHERE m.channel_id = ch.id
			  AND m.created_at > COALESCE(cr.last_read_at, '-infinity'::timestamptz)
			  AND NOT (m.author_type = 'user' AND m.author_id = $2)
		) uc ON true
		WHERE ch.workspace_id = $1 AND ch.kind = 'group'
		ORDER BY ch.updated_at DESC, ch.created_at DESC`, parseUUID(workspaceID), uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	defer rows.Close()
	out := []ChannelResponse{}
	channelIDs := []pgtype.UUID{}
	for rows.Next() {
		var id, wsID, createdBy pgtype.UUID
		var name string
		var desc, lark, lastType, lastName, lastContent pgtype.Text
		var createdAt, updatedAt, lastAt pgtype.Timestamptz
		var unread int
		var kind string
		if err := rows.Scan(&id, &wsID, &name, &desc, &lark, &createdBy, &createdAt, &updatedAt, &kind,
			&lastType, &lastName, &lastContent, &lastAt, &unread); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channels")
			return
		}
		ch := ChannelResponse{
			ID: uuidToString(id), WorkspaceID: uuidToString(wsID), Name: name,
			Description: textToPtr(desc), LarkChatID: textToPtr(lark), CreatedBy: uuidToString(createdBy),
			CreatedAt: timestampToString(createdAt), UpdatedAt: timestampToString(updatedAt),
			Kind: kind, UnreadCount: unread, Members: []ChannelMemberBrief{},
		}
		if lastContent.Valid {
			ch.LastMessage = &ChannelLastMessage{
				AuthorType: lastType.String, AuthorName: lastName.String,
				Content: lastContent.String, CreatedAt: timestampToString(lastAt),
			}
		}
		out = append(out, ch)
		channelIDs = append(channelIDs, id)
	}
	rows.Close()

	// Second pass: members for the avatar stack, grouped by channel.
	if len(channelIDs) > 0 {
		memberRows, err := h.DB.Query(r.Context(), `
			SELECT cm.channel_id, cm.member_type, cm.member_id, COALESCE(u.name, u.email, a.name, '')
			FROM channel_member cm
			LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
			LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
			WHERE cm.channel_id = ANY($1::uuid[]) AND cm.workspace_id = $2
			ORDER BY cm.created_at ASC`, channelIDs, parseUUID(workspaceID))
		if err == nil {
			defer memberRows.Close()
			grouped := map[string][]ChannelMemberBrief{}
			for memberRows.Next() {
				var chID, memberID pgtype.UUID
				var memberType, memberName string
				if err := memberRows.Scan(&chID, &memberType, &memberID, &memberName); err != nil {
					continue
				}
				key := uuidToString(chID)
				grouped[key] = append(grouped[key], ChannelMemberBrief{MemberType: memberType, MemberID: uuidToString(memberID), Name: memberName})
			}
			for i := range out {
				if m := grouped[out[i].ID]; m != nil {
					out[i].Members = m
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) MarkChannelRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	_, err := h.DB.Exec(r.Context(), `
		INSERT INTO channel_read (channel_id, user_id, last_read_at)
		VALUES ($1, $2, now())
		ON CONFLICT (channel_id, user_id) DO UPDATE SET last_read_at = now()`, channelID, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark channel read")
		return
	}
	h.clearDMPeerManualUnreadForChannel(r.Context(), workspaceID, userID, channelID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	var req CreateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len([]rune(name)) > channelNameMaxLen {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	desc := trimTextPtr(req.Description)
	larkChatID := trimTextPtr(req.LarkChatID)
	row := h.DB.QueryRow(r.Context(), `
		INSERT INTO channel (workspace_id, name, description, lark_chat_id, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind`,
		parseUUID(workspaceID), name, desc, larkChatID, parseUUID(userID))
	ch, err := scanChannel(row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create channel")
		return
	}
	_, _ = h.DB.Exec(r.Context(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)
		ON CONFLICT DO NOTHING`, parseUUID(ch.ID), parseUUID(workspaceID), parseUUID(userID))
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, ch)
	writeJSON(w, http.StatusCreated, ch)
}

func (h *Handler) UpdateChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	var req UpdateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var name *string
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if len([]rune(trimmed)) > channelNameMaxLen {
			writeError(w, http.StatusBadRequest, "name is too long")
			return
		}
		name = &trimmed
	}
	row := h.DB.QueryRow(r.Context(), `
		UPDATE channel
		SET name = COALESCE($3, name), description = COALESCE($4, description), lark_chat_id = COALESCE($5, lark_chat_id), updated_at = now()
		WHERE id = $1 AND workspace_id = $2
		RETURNING id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind`,
		channelID, parseUUID(workspaceID), name, trimTextPtr(req.Description), trimTextPtr(req.LarkChatID))
	ch, err := scanChannel(row)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update channel")
		return
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, ch)
	writeJSON(w, http.StatusOK, ch)
}

func (h *Handler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	// FK ON DELETE CASCADE clears every dependent row (channel_member,
	// channel_message, channel_agent_session, channel_read, and attachment
	// rows scoped to this channel). The agents' chat_session rows are not
	// channel-owned and intentionally survive, mirroring 1:1 chat.
	tag, err := h.DB.Exec(r.Context(),
		`DELETE FROM channel WHERE id = $1 AND workspace_id = $2`,
		channelID, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete channel")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	h.publish(protocol.EventChannelDeleted, workspaceID, "member", userID, map[string]any{"id": uuidToString(channelID)})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListChannelMembers(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT cm.member_type, cm.member_id,
		       COALESCE(u.name, u.email, a.name, ''), cm.created_at
		FROM channel_member cm
		LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
		LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2
		ORDER BY cm.created_at ASC`, channelID, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel members")
		return
	}
	defer rows.Close()
	out := []ChannelMemberResponse{}
	for rows.Next() {
		var typ, name string
		var id pgtype.UUID
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&typ, &id, &name, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel members")
			return
		}
		out = append(out, ChannelMemberResponse{MemberType: typ, MemberID: uuidToString(id), Name: name, CreatedAt: timestampToString(createdAt)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AddChannelMember(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var req AddChannelMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	memberID, ok := parseUUIDOrBadRequest(w, req.MemberID, "member_id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.validateChannelMemberTarget(w, r, workspaceID, req.MemberType, memberID) {
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, channelID, parseUUID(workspaceID), req.MemberType, memberID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add channel member")
		return
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, map[string]any{"id": uuidToString(channelID)})
	// When a NEW human joins, every agent member greets them with a sticker.
	// Guarded on RowsAffected so a duplicate re-add never re-welcomes, and on
	// member_type so adding an agent member (or the creator at channel creation,
	// which never routes through here) stays silent.
	if req.MemberType == "user" && tag.RowsAffected() > 0 {
		h.dispatchChannelMemberWelcome(r.Context(), workspaceID, channelID, memberID, parseUUID(userID))
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (h *Handler) RemoveChannelMember(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	memberType := chi.URLParam(r, "memberType")
	memberID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "memberId"), "member id")
	if !ok {
		return
	}
	if memberType != "user" && memberType != "agent" {
		writeError(w, http.StatusBadRequest, "member_type must be user or agent")
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	_, err := h.DB.Exec(r.Context(), `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = $3 AND member_id = $4`,
		channelID, parseUUID(workspaceID), memberType, memberID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove channel member")
		return
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, map[string]any{"id": uuidToString(channelID)})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ListChannelMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id, thread_id, trigger_depth, created_at
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2
		ORDER BY created_at ASC`, channelID, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel messages")
		return
	}
	defer rows.Close()
	out := []ChannelMessageResponse{}
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel messages")
			return
		}
		out = append(out, msg)
	}
	messageIDs := make([]pgtype.UUID, len(out))
	for i, m := range out {
		messageIDs[i] = parseUUID(m.ID)
	}
	grouped := h.groupChannelMessageAttachments(r.Context(), workspaceID, messageIDs)
	for i := range out {
		out[i].Attachments = grouped[out[i].ID]
	}
	writeJSON(w, http.StatusOK, out)
}

type ChannelAuthorStat struct {
	AuthorType string  `json:"author_type"`
	AuthorID   *string `json:"author_id"`
	AuthorName string  `json:"author_name"`
	Count      int     `json:"count"`
}

type ChannelStatsResponse struct {
	TotalMessages int                 `json:"total_messages"`
	FileCount     int                 `json:"file_count"`
	MemberCount   int                 `json:"member_count"`
	ByAuthor      []ChannelAuthorStat `json:"by_author"`
}

func (h *Handler) ListChannelAttachments(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	attachments, err := h.Queries.ListAttachmentsByChannel(r.Context(), db.ListAttachmentsByChannelParams{
		ChannelID:   channelID,
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel attachments")
		return
	}
	out := make([]AttachmentResponse, 0, len(attachments))
	for _, a := range attachments {
		out = append(out, h.attachmentToResponse(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) GetChannelStats(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	resp := ChannelStatsResponse{ByAuthor: []ChannelAuthorStat{}}
	wsID := parseUUID(workspaceID)

	_ = h.DB.QueryRow(r.Context(), `SELECT count(*) FROM channel_message WHERE channel_id = $1 AND workspace_id = $2`, channelID, wsID).Scan(&resp.TotalMessages)
	_ = h.DB.QueryRow(r.Context(), `SELECT count(*) FROM attachment WHERE channel_id = $1 AND workspace_id = $2`, channelID, wsID).Scan(&resp.FileCount)
	_ = h.DB.QueryRow(r.Context(), `SELECT count(*) FROM channel_member WHERE channel_id = $1 AND workspace_id = $2`, channelID, wsID).Scan(&resp.MemberCount)

	rows, err := h.DB.Query(r.Context(), `
		SELECT author_type, author_id, author_name, count(*)
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2
		GROUP BY author_type, author_id, author_name
		ORDER BY count(*) DESC`, channelID, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute channel stats")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var stat ChannelAuthorStat
		var authorID pgtype.UUID
		if err := rows.Scan(&stat.AuthorType, &authorID, &stat.AuthorName, &stat.Count); err != nil {
			continue
		}
		stat.AuthorID = uuidToPtr(authorID)
		resp.ByAuthor = append(resp.ByAuthor, stat)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) SetChannelTyping(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var req ChannelTypingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	h.publish(protocol.EventChannelTyping, workspaceID, "member", userID, protocol.ChannelTypingPayload{
		ChannelID:   uuidToString(channelID),
		ActorType:   "user",
		ActorID:     userID,
		ActorName:   h.channelAuthorName(r.Context(), userID),
		IsTyping:    req.IsTyping,
		ExpiresInMS: channelUserTypingExpiresInMS,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) SendChannelMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var req SendChannelMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	// Pre-validate attachment ids early so invalid input returns 400 before
	// any state mutation. The actual link runs after insert so we have a
	// message_id to back-fill into the attachment rows.
	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, req.AttachmentIDs, "attachment_ids")
	if !ok {
		return
	}
	ch, found := h.getChannel(r.Context(), workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	authorName := h.channelAuthorName(r.Context(), userID)
	threadID := uuid.NewString()
	msg, err := h.insertChannelMessage(r.Context(), channelID, parseUUID(workspaceID), "user", parseUUID(userID), authorName, content, "multica", nil, &threadID, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create channel message")
		return
	}
	// Back-fill channel_message_id on attachments uploaded against this
	// channel while composing. The query only touches rows where channel_id
	// matches AND channel_message_id IS NULL, so it cannot rebind an
	// attachment that already belongs to an earlier message.
	if len(attachmentIDs) > 0 {
		if err := h.Queries.LinkAttachmentsToChannelMessage(r.Context(), db.LinkAttachmentsToChannelMessageParams{
			ChannelMessageID: parseUUID(msg.ID),
			ChannelID:        channelID,
			Column3:          attachmentIDs,
		}); err != nil {
			// Don't fail the send — the message is saved and the files remain
			// on the channel (still downloadable).
			slog.Warn("link channel attachments failed", "error", err, "message_id", msg.ID)
		} else {
			msg.Attachments = h.groupChannelMessageAttachments(r.Context(), workspaceID, []pgtype.UUID{parseUUID(msg.ID)})[msg.ID]
		}
	}
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, channelID)
	if ch.Kind == "dm" {
		h.clearDMHiddenForChannelMembers(r.Context(), workspaceID, channelID)
	}
	h.publish(protocol.EventChannelMessage, workspaceID, "member", userID, msg)
	if ch.Kind == "dm" {
		// 1-on-1 DM: the agent peer (if any) replies to every user message
		// without an @-mention. Human↔human DMs have no agent member → no-op.
		h.dispatchDMAgentReply(r.Context(), ch, msg, parseUUID(userID))
	} else {
		h.dispatchChannelMentions(r.Context(), ch, msg, parseUUID(userID))
	}
	h.sendChannelMessageToFeishu(r.Context(), ch, authorName, content)
	writeJSON(w, http.StatusCreated, msg)
}

func (h *Handler) ImportLarkChannelMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	var req ImportLarkChannelMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	larkChatID := strings.TrimSpace(req.LarkChatID)
	content := strings.TrimSpace(req.Content)
	if larkChatID == "" || content == "" {
		writeError(w, http.StatusBadRequest, "lark_chat_id and content are required")
		return
	}
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	authorName := strings.TrimSpace(req.AuthorName)
	if authorName == "" {
		authorName = "Feishu"
	}
	externalID := strings.TrimSpace(req.ExternalMessageID)
	var external *string
	if externalID != "" {
		external = &externalID
	}

	ch, found := h.getChannelByLarkChatID(r.Context(), workspaceID, larkChatID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	threadID := uuid.NewString()
	msg, err := h.insertChannelMessage(r.Context(), parseUUID(ch.ID), parseUUID(workspaceID), "lark", pgtype.UUID{}, authorName, content, "lark", external, &threadID, 0)
	if err != nil {
		if errorsIsNoRows(err) || isUniqueViolation(err) {
			writeError(w, http.StatusNotFound, "channel not found or message already imported")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to import lark message")
		return
	}
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, parseUUID(msg.ChannelID))
	h.publish(protocol.EventChannelMessage, workspaceID, "member", userID, msg)
	h.dispatchChannelMentions(r.Context(), ch, msg, parseUUID(userID))
	writeJSON(w, http.StatusCreated, msg)
}

func (h *Handler) validateChannelMemberTarget(w http.ResponseWriter, r *http.Request, workspaceID, memberType string, memberID pgtype.UUID) bool {
	switch memberType {
	case "user":
		if _, err := h.getWorkspaceMember(r.Context(), uuidToString(memberID), workspaceID); err != nil {
			writeError(w, http.StatusNotFound, "workspace member not found")
			return false
		}
		return true
	case "agent":
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: memberID, WorkspaceID: parseUUID(workspaceID)})
		if err != nil || agent.ArchivedAt.Valid {
			writeError(w, http.StatusNotFound, "agent not found")
			return false
		}
		userID, _ := requireUserID(w, r)
		actorType, actorID := h.resolveActor(r, userID, workspaceID)
		if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
			writeError(w, http.StatusForbidden, "you do not have access to this agent")
			return false
		}
		return true
	default:
		writeError(w, http.StatusBadRequest, "member_type must be user or agent")
		return false
	}
}

func (h *Handler) dispatchChannelMentions(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	// Notify mentioned humans regardless of the agent trigger limit — surfacing a
	// mention to a person never feeds the automatic agent-reply loop.
	h.notifyChannelMemberMentions(ctx, ch, trigger)
	for _, agent := range h.channelMentionedAgents(ctx, ch.WorkspaceID, ch.ID, trigger.Content) {
		h.dispatchChannelAgentReply(ctx, ch, agent, trigger, initiatorUserID)
	}
}

// dispatchChannelAgentReply runs one agent's reply to a triggering message:
// ensure the channel<->agent chat session, persist the user-role prompt, enqueue
// the agent task, and tag the prompt with the task. Shared by @-mention dispatch
// (group channels) and DM auto-dispatch (1-on-1 channel whose peer is an agent).
//
// Two guards keep the agent-reply loop bounded and prevent self-conversation and
// MUST be preserved for both callers:
//   - trigger-depth limit: an agent reply that itself re-triggers stops at the limit.
//   - self-trigger skip: an agent's own message never re-triggers that same agent
//     (otherwise a 1-on-1 agent DM would loop on the agent's own replies forever).
func (h *Handler) dispatchChannelAgentReply(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	if trigger.TriggerDepth >= channelRunTriggerLimit {
		slog.Warn("channel agent reply: trigger limit reached", "channel", ch.ID, "thread_id", ptrString(trigger.ThreadID), "depth", trigger.TriggerDepth)
		return
	}
	if trigger.AuthorType == "agent" && trigger.AuthorID != nil && *trigger.AuthorID == uuidToString(agent.ID) {
		return
	}
	h.publishChannelAgentTyping(ch, agent, true)
	session, err := h.ensureChannelAgentSession(ctx, ch, agent.ID, initiatorUserID)
	if err != nil {
		slog.Warn("channel agent reply: ensure chat session failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	prompt := h.buildChannelMentionPrompt(ctx, ch, trigger)
	promptMsg, err := h.createChannelAgentPromptMessage(ctx, session.ID, prompt, trigger)
	if err != nil {
		slog.Warn("channel agent reply: create chat message failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	task, err := h.TaskService.EnqueueChatTask(ctx, session, initiatorUserID)
	if err != nil {
		slog.Warn("channel agent reply: enqueue chat task failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	if _, err := h.DB.Exec(ctx, `UPDATE chat_message SET task_id = $1 WHERE id = $2`, task.ID, promptMsg.ID); err != nil {
		slog.Warn("channel agent reply: tag prompt with task failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "task", uuidToString(task.ID), "error", err)
	}
}

// dispatchChannelMemberWelcome makes every agent member of a channel post a
// short, sticker-led welcome when a new human joins. Each welcome is an
// independent one-off agent run on its own fresh thread, driven by a static
// prompt (no channel history) that forbids @-mentions — so welcomes never react
// to each other and never chain into the automatic agent-reply discussion loop.
func (h *Handler) dispatchChannelMemberWelcome(ctx context.Context, workspaceID string, channelID, joinedUserID, initiatorUserID pgtype.UUID) {
	agents := h.channelAgentMembers(ctx, workspaceID, uuidToString(channelID))
	if len(agents) == 0 {
		return
	}
	var channelName string
	if err := h.DB.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, channelID).Scan(&channelName); err != nil {
		slog.Warn("channel welcome: load channel name failed", "channel", uuidToString(channelID), "error", err)
		return
	}
	ch := ChannelResponse{ID: uuidToString(channelID), WorkspaceID: workspaceID, Name: channelName}
	joinedName := strings.TrimSpace(h.userName(ctx, joinedUserID))
	if joinedName == "" {
		joinedName = "新成员"
	}
	prompt := buildChannelWelcomePrompt(ch.Name, joinedName)
	// Synthetic trigger: fresh thread (nil ThreadID) at depth 0, so each welcome
	// is its own short run rather than a reply within an existing thread.
	synthetic := ChannelMessageResponse{TriggerDepth: 0}
	for _, agent := range agents {
		h.publishChannelAgentTyping(ch, agent, true)
		session, err := h.ensureChannelAgentSession(ctx, ch, agent.ID, initiatorUserID)
		if err != nil {
			slog.Warn("channel welcome: ensure session failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
			continue
		}
		promptMsg, err := h.createChannelAgentPromptMessage(ctx, session.ID, prompt, synthetic)
		if err != nil {
			slog.Warn("channel welcome: create prompt failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
			continue
		}
		task, err := h.TaskService.EnqueueChatTask(ctx, session, initiatorUserID)
		if err != nil {
			slog.Warn("channel welcome: enqueue task failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
			continue
		}
		if _, err := h.DB.Exec(ctx, `UPDATE chat_message SET task_id = $1 WHERE id = $2`, task.ID, promptMsg.ID); err != nil {
			slog.Warn("channel welcome: tag prompt with task failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "task", uuidToString(task.ID), "error", err)
		}
	}
}

// buildChannelWelcomePrompt is a self-contained one-off greeting prompt. Unlike
// buildChannelMentionPrompt it includes NO channel history and explicitly bans
// @-mentions and follow-up, so a wall of welcomes never turns into a loop.
func buildChannelWelcomePrompt(channelName, joinedName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "A new member just joined the Multica group chat #%s: %s.\n", channelName, joinedName)
	b.WriteString("Greet them as yourself with a warm, friendly welcome.\n\n")
	b.WriteString("Rules — follow all of them:\n")
	b.WriteString("- Keep it to ONE short line, in the language the channel uses (Chinese if the member's name is Chinese).\n")
	fmt.Fprintf(&b, "- Include exactly one sticker via the multica-stickers skill (e.g. :sticker:applause:, :sticker:hi:, :sticker:heart-hands:) — the sticker is the point of welcoming %s.\n", joinedName)
	b.WriteString("- Do NOT @-mention anyone — not the new member, not other agents. This is a one-off greeting, not a discussion.\n")
	b.WriteString("- Do not ask questions, assign work, or start a conversation. Just welcome them in one line and stop.\n")
	return b.String()
}

// notifyChannelMemberMentions creates a "mentioned" inbox item for every human
// channel member @-mentioned in a channel message (by an agent, another member,
// or via @all), so the mention surfaces in the recipient's overview "for me"
// list with a deep link back to the message. The message author is never
// notified about their own mention.
func (h *Handler) notifyChannelMemberMentions(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse) {
	mentions := util.ParseMentions(msg.Content)
	if len(mentions) == 0 {
		return
	}
	members := h.channelHumanMemberIDs(ctx, ch.WorkspaceID, ch.ID)
	if len(members) == 0 {
		return
	}

	recipients := map[string]bool{}
	for _, m := range mentions {
		switch m.Type {
		case "all":
			for id := range members {
				recipients[id] = true
			}
		case "member":
			if members[m.ID] {
				recipients[m.ID] = true
			}
		}
	}
	// Never notify the author about their own message.
	if msg.AuthorID != nil {
		delete(recipients, *msg.AuthorID)
	}
	if len(recipients) == 0 {
		return
	}

	// inbox_item.actor_type is constrained to member|agent|system.
	actorType := "system"
	var actorID pgtype.UUID
	switch msg.AuthorType {
	case "user":
		actorType = "member"
		if msg.AuthorID != nil {
			actorID = parseUUID(*msg.AuthorID)
		}
	case "agent":
		actorType = "agent"
		if msg.AuthorID != nil {
			actorID = parseUUID(*msg.AuthorID)
		}
	}

	details, _ := json.Marshal(map[string]string{
		"channel_id":   ch.ID,
		"channel_name": ch.Name,
		"message_id":   msg.ID,
		"actor_name":   msg.AuthorName,
	})
	body := strings.TrimSpace(msg.Content)
	if runes := []rune(body); len(runes) > 280 {
		body = string(runes[:280])
	}
	title := fmt.Sprintf("%s mentioned you in #%s", msg.AuthorName, ch.Name)

	for id := range recipients {
		item, err := h.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
			WorkspaceID:   parseUUID(ch.WorkspaceID),
			RecipientType: "member",
			RecipientID:   parseUUID(id),
			Type:          "mentioned",
			Severity:      "info",
			Title:         title,
			Body:          strToText(body),
			ActorType:     strToText(actorType),
			ActorID:       actorID,
			Details:       details,
		})
		if err != nil {
			slog.Warn("channel mention: inbox creation failed", "channel", ch.ID, "recipient", id, "error", err)
			continue
		}
		h.publish(protocol.EventInboxNew, ch.WorkspaceID, actorType, uuidToString(actorID), map[string]any{"item": inboxToResponse(item)})
	}
}

// channelHumanMemberIDs returns the set of human (user) member IDs in a channel.
func (h *Handler) channelHumanMemberIDs(ctx context.Context, workspaceID, channelID string) map[string]bool {
	rows, err := h.DB.Query(ctx, `
		SELECT member_id
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user'`,
		parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		out[uuidToString(id)] = true
	}
	return out
}

func (h *Handler) publishChannelAgentTyping(ch ChannelResponse, agent db.Agent, isTyping bool) {
	h.publish(protocol.EventChannelTyping, ch.WorkspaceID, "agent", uuidToString(agent.ID), protocol.ChannelTypingPayload{
		ChannelID:   ch.ID,
		ActorType:   "agent",
		ActorID:     uuidToString(agent.ID),
		ActorName:   agent.Name,
		IsTyping:    isTyping,
		ExpiresInMS: channelAgentTypingExpiresInMS,
	})
}

func (h *Handler) channelMentionedAgents(ctx context.Context, workspaceID, channelID, content string) []db.Agent {
	mentionAll := contentMentionsAll(content)
	rows, err := h.DB.Query(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.visibility, a.status,
		       a.max_concurrent_tasks, a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.archived_at
		FROM channel_member cm
		JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2 AND a.archived_at IS NULL`, parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []db.Agent
	for rows.Next() {
		var a db.Agent
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.AvatarUrl, &a.RuntimeMode, &a.RuntimeConfig, &a.Visibility, &a.Status, &a.MaxConcurrentTasks, &a.OwnerID, &a.CreatedAt, &a.UpdatedAt, &a.Description, &a.RuntimeID, &a.ArchivedAt); err != nil {
			continue
		}
		if mentionAll || contentMentionsAgent(content, a.Name) {
			out = append(out, a)
		}
	}
	return out
}

func contentMentionsAll(content string) bool {
	return strings.Contains(strings.ToLower(content), "@all")
}

func contentMentionsAgent(content, name string) bool {
	needle := "@" + strings.ToLower(strings.TrimSpace(name))
	if needle == "@" {
		return false
	}
	return strings.Contains(strings.ToLower(content), needle)
}

func (h *Handler) buildChannelMentionPrompt(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse) string {
	members := h.channelMemberSummaries(ctx, ch.WorkspaceID, ch.ID)
	messages := h.recentChannelMessages(ctx, ch.WorkspaceID, ch.ID, channelContextMessageLimit)

	var b strings.Builder
	fmt.Fprintf(&b, "You are participating in the Multica group chat #%s.\n", ch.Name)
	b.WriteString("Only respond as yourself. Do not impersonate other agents or users.\n")
	b.WriteString("Use the recent channel context below, but answer the current mention directly.\n")
	b.WriteString("This is a collaborative discussion — keep it going until the topic is actually resolved, not just one exchange. ")
	b.WriteString("If the discussion is not finished (you need input, have a follow-up question, disagree, or want to push the topic forward), END your reply by @-mentioning the specific member(s) you want to continue with, using their exact mention links as listed below. You may @ several members at once. ")
	b.WriteString("Only stop @-mentioning when you have reached a final conclusion and there is genuinely nothing left to discuss — a one-line acknowledgement is not a conclusion.\n")
	fmt.Fprintf(&b, "To prevent runaway loops, this channel run is limited to %d automatic agent turns; current trigger depth is %d. As you near the limit, steer the discussion toward a concrete conclusion.\n\n", channelRunTriggerLimit, trigger.TriggerDepth)
	if len(members) > 0 {
		// Give the exact mention link per member (humans included), not just a
		// name. The model reliably linkifies agents because their links recur in
		// channel history, but humans were getting bare "@name" text — which the
		// UI can't colorize or route. Handing over the verbatim link makes every
		// mention a real, colored, routable tag.
		b.WriteString("Channel members — to @-mention one, copy its mention link verbatim (never write a bare @name):\n")
		for _, member := range members {
			mentionType := member.MemberType
			if mentionType == "user" {
				mentionType = "member"
			}
			fmt.Fprintf(&b, "- %s (%s): [@%s](mention://%s/%s)\n", member.Name, member.MemberType, member.Name, mentionType, member.MemberID)
		}
		b.WriteString("\n")
	}
	if len(messages) > 0 {
		b.WriteString("Recent channel messages:\n")
		for _, msg := range messages {
			fmt.Fprintf(&b, "[%s] %s (%s): %s\n", msg.CreatedAt, msg.AuthorName, msg.AuthorType, msg.Content)
		}
		b.WriteString("\n")
	}
	b.WriteString("Current message to respond to:\n")
	fmt.Fprintf(&b, "%s (%s): %s", trigger.AuthorName, trigger.AuthorType, trigger.Content)
	return b.String()
}

func (h *Handler) channelMemberSummaries(ctx context.Context, workspaceID, channelID string) []ChannelMemberResponse {
	rows, err := h.DB.Query(ctx, `
		SELECT cm.member_type, cm.member_id,
		       COALESCE(u.name, u.email, a.name, ''), cm.created_at
		FROM channel_member cm
		LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
		LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2
		ORDER BY cm.created_at ASC`, parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ChannelMemberResponse
	for rows.Next() {
		var typ, name string
		var id pgtype.UUID
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&typ, &id, &name, &createdAt); err != nil {
			continue
		}
		out = append(out, ChannelMemberResponse{MemberType: typ, MemberID: uuidToString(id), Name: name, CreatedAt: timestampToString(createdAt)})
	}
	return out
}

func (h *Handler) recentChannelMessages(ctx context.Context, workspaceID, channelID string, limit int) []ChannelMessageResponse {
	rows, err := h.DB.Query(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id, thread_id, trigger_depth, created_at
		FROM (
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id, thread_id, trigger_depth, created_at
			FROM channel_message
			WHERE channel_id = $1 AND workspace_id = $2
			ORDER BY created_at DESC
			LIMIT $3
		) recent
		ORDER BY created_at ASC`, parseUUID(channelID), parseUUID(workspaceID), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ChannelMessageResponse
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func (h *Handler) createChannelAgentPromptMessage(ctx context.Context, chatSessionID pgtype.UUID, prompt string, trigger ChannelMessageResponse) (db.ChatMessage, error) {
	threadID := trigger.ThreadID
	if threadID == nil || strings.TrimSpace(*threadID) == "" {
		fresh := uuid.NewString()
		threadID = &fresh
	}
	row := h.DB.QueryRow(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, thread_id, trigger_depth)
		VALUES ($1, 'user', $2, $3, $4)
		RETURNING id, chat_session_id, role, content, task_id, created_at, failure_reason, elapsed_ms`, chatSessionID, prompt, threadID, trigger.TriggerDepth)
	var msg db.ChatMessage
	err := row.Scan(&msg.ID, &msg.ChatSessionID, &msg.Role, &msg.Content, &msg.TaskID, &msg.CreatedAt, &msg.FailureReason, &msg.ElapsedMs)
	return msg, err
}

func (h *Handler) ensureChannelAgentSession(ctx context.Context, ch ChannelResponse, agentID pgtype.UUID, creatorID pgtype.UUID) (db.ChatSession, error) {
	var sessionID pgtype.UUID
	err := h.DB.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, parseUUID(ch.ID), agentID).Scan(&sessionID)
	if err == nil {
		return h.Queries.GetChatSession(ctx, sessionID)
	}
	if !errorsIsNoRows(err) {
		return db.ChatSession{}, err
	}
	session, err := h.Queries.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: parseUUID(ch.WorkspaceID),
		AgentID:     agentID,
		CreatorID:   creatorID,
		Title:       "#" + ch.Name,
	})
	if err != nil {
		return db.ChatSession{}, err
	}
	_, err = h.DB.Exec(ctx, `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (channel_id, agent_id) DO NOTHING`, parseUUID(ch.ID), agentID, session.ID)
	if err != nil {
		return db.ChatSession{}, err
	}
	return session, nil
}

func (h *Handler) handleChannelChatStopped(e events.Event) {
	chatSessionID, _ := e.Payload.(map[string]any)["chat_session_id"].(string)
	if chatSessionID == "" {
		return
	}
	channelID, workspaceID, agentID, ok := h.channelAgentForChatSession(context.Background(), chatSessionID)
	if !ok {
		return
	}
	h.publish(protocol.EventChannelTyping, uuidToString(workspaceID), "agent", uuidToString(agentID), protocol.ChannelTypingPayload{
		ChannelID: uuidToString(channelID),
		ActorType: "agent",
		ActorID:   uuidToString(agentID),
		ActorName: h.agentName(context.Background(), agentID),
		IsTyping:  false,
	})
}

func (h *Handler) handleChannelChatDone(e events.Event) {
	payload, ok := e.Payload.(protocol.ChatDonePayload)
	if !ok || payload.ChatSessionID == "" || strings.TrimSpace(payload.Content) == "" {
		return
	}
	channelID, workspaceID, agentID, ok := h.channelAgentForChatSession(context.Background(), payload.ChatSessionID)
	if !ok {
		return
	}
	var taskID pgtype.UUID
	if strings.TrimSpace(payload.TaskID) != "" {
		taskID = parseUUID(payload.TaskID)
	}
	threadID, triggerDepth := h.channelThreadForChatTask(context.Background(), parseUUID(payload.ChatSessionID), taskID)
	nextDepth := triggerDepth + 1
	agentName := h.agentName(context.Background(), agentID)
	h.publish(protocol.EventChannelTyping, uuidToString(workspaceID), "agent", uuidToString(agentID), protocol.ChannelTypingPayload{
		ChannelID: uuidToString(channelID),
		ActorType: "agent",
		ActorID:   uuidToString(agentID),
		ActorName: agentName,
		IsTyping:  false,
	})
	msg, err := h.insertChannelMessage(context.Background(), channelID, workspaceID, "agent", agentID, agentName, payload.Content, "multica", nil, threadID, nextDepth)
	if err != nil {
		slog.Warn("channel bridge: insert agent reply failed", "chat_session_id", payload.ChatSessionID, "error", err)
		return
	}
	_, _ = h.DB.Exec(context.Background(), `UPDATE channel SET updated_at = now() WHERE id = $1`, channelID)
	h.clearDMHiddenForChannelMembers(context.Background(), uuidToString(workspaceID), channelID)
	h.publish(protocol.EventChannelMessage, uuidToString(workspaceID), "agent", uuidToString(agentID), msg)
	ch, found := h.getChannel(context.Background(), uuidToString(workspaceID), channelID)
	if found {
		h.dispatchChannelMentions(context.Background(), ch, msg, h.channelInitiatorForChatSession(context.Background(), parseUUID(payload.ChatSessionID)))
		h.sendChannelMessageToFeishu(context.Background(), ch, agentName, payload.Content)
	}
}

func (h *Handler) channelAgentForChatSession(ctx context.Context, chatSessionID string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, bool) {
	var channelID, workspaceID, agentID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT cas.channel_id, ch.workspace_id, cas.agent_id
		FROM channel_agent_session cas
		JOIN channel ch ON ch.id = cas.channel_id
		WHERE cas.chat_session_id = $1`, parseUUID(chatSessionID)).Scan(&channelID, &workspaceID, &agentID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	return channelID, workspaceID, agentID, true
}

func (h *Handler) channelThreadForChatTask(ctx context.Context, chatSessionID, taskID pgtype.UUID) (*string, int) {
	if taskID.Valid {
		if threadID, depth, ok := h.channelThreadFromQuery(ctx, `
			SELECT thread_id, trigger_depth
			FROM chat_message
			WHERE chat_session_id = $1 AND task_id = $2 AND role = 'user'
			ORDER BY created_at DESC
			LIMIT 1`, chatSessionID, taskID); ok {
			return threadID, depth
		}
	}
	if threadID, depth, ok := h.channelThreadFromQuery(ctx, `
		SELECT thread_id, trigger_depth
		FROM chat_message
		WHERE chat_session_id = $1 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, chatSessionID); ok {
		return threadID, depth
	}
	fresh := uuid.NewString()
	return &fresh, 0
}

func (h *Handler) channelThreadFromQuery(ctx context.Context, query string, args ...any) (*string, int, bool) {
	var thread pgtype.Text
	var depth int
	err := h.DB.QueryRow(ctx, query, args...).Scan(&thread, &depth)
	if err != nil || !thread.Valid || strings.TrimSpace(thread.String) == "" {
		return nil, 0, false
	}
	return &thread.String, depth, true
}

func (h *Handler) channelInitiatorForChatSession(ctx context.Context, chatSessionID pgtype.UUID) pgtype.UUID {
	var initiator pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT initiator_user_id
		FROM agent_task_queue
		WHERE chat_session_id = $1 AND initiator_user_id IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 1`, chatSessionID).Scan(&initiator)
	if err == nil && initiator.Valid {
		return initiator
	}
	var creator pgtype.UUID
	if err := h.DB.QueryRow(ctx, `SELECT creator_id FROM chat_session WHERE id = $1`, chatSessionID).Scan(&creator); err == nil {
		return creator
	}
	return pgtype.UUID{}
}

func (h *Handler) insertChannelMessage(ctx context.Context, channelID, workspaceID pgtype.UUID, authorType string, authorID pgtype.UUID, authorName, content, source string, externalID *string, threadID *string, triggerDepth int) (ChannelMessageResponse, error) {
	row := h.DB.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id, thread_id, trigger_depth)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id, thread_id, trigger_depth, created_at`,
		channelID, workspaceID, authorType, nullableUUID(authorID), authorName, content, source, externalID, threadID, triggerDepth)
	return scanChannelMessage(row)
}

func (h *Handler) sendChannelMessageToFeishu(ctx context.Context, ch ChannelResponse, authorName, content string) {
	if ch.LarkChatID == nil || h.LarkAPIClient == nil || h.LarkInstallations == nil || !h.LarkAPIClient.IsConfigured() {
		return
	}
	inst, ok := h.firstActiveFeishuInstallation(ctx, ch.WorkspaceID)
	if !ok {
		return
	}
	secret, err := h.LarkInstallations.DecryptAppSecret(inst)
	if err != nil {
		slog.Warn("channel feishu sync: decrypt app secret failed", "error", err)
		return
	}
	creds := lark.InstallationCredentials{AppID: inst.AppID, AppSecret: secret, TenantKey: inst.TenantKey.String, Region: lark.RegionOrDefault(inst.Region)}
	text := strings.TrimSpace(authorName + ": " + content)
	_, err = h.LarkAPIClient.SendTextMessage(ctx, lark.SendTextParams{InstallationID: creds, ChatID: lark.ChatID(*ch.LarkChatID), Text: text})
	if err != nil {
		slog.Warn("channel feishu sync: send text failed", "channel", ch.ID, "error", err)
	}
}

func (h *Handler) firstActiveFeishuInstallation(ctx context.Context, workspaceID string) (db.LarkInstallation, bool) {
	rows, err := h.Queries.ListLarkInstallationsByWorkspace(ctx, parseUUID(workspaceID))
	if err != nil {
		return db.LarkInstallation{}, false
	}
	for _, row := range rows {
		if row.Status == "active" && lark.RegionOrDefault(row.Region) == lark.RegionFeishu {
			return row, true
		}
	}
	return db.LarkInstallation{}, false
}

func (h *Handler) channelExists(ctx context.Context, workspaceID string, channelID pgtype.UUID) bool {
	var id pgtype.UUID
	return h.DB.QueryRow(ctx, `SELECT id FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID)).Scan(&id) == nil
}

func (h *Handler) channelUserIsMember(ctx context.Context, workspaceID string, channelID, userID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_member
			WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3
		)`, channelID, parseUUID(workspaceID), userID).Scan(&exists)
	return err == nil && exists
}

func (h *Handler) requireChannelUserMember(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID, userID pgtype.UUID) bool {
	if !h.channelExists(ctx, workspaceID, channelID) {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	if !h.channelUserIsMember(ctx, workspaceID, channelID, userID) {
		writeError(w, http.StatusForbidden, "not a channel member")
		return false
	}
	return true
}

func (h *Handler) getChannel(ctx context.Context, workspaceID string, channelID pgtype.UUID) (ChannelResponse, bool) {
	row := h.DB.QueryRow(ctx, `SELECT id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID))
	ch, err := scanChannel(row)
	return ch, err == nil
}

func (h *Handler) getChannelByLarkChatID(ctx context.Context, workspaceID, larkChatID string) (ChannelResponse, bool) {
	row := h.DB.QueryRow(ctx, `SELECT id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind FROM channel WHERE workspace_id = $1 AND lark_chat_id = $2 LIMIT 1`, parseUUID(workspaceID), larkChatID)
	ch, err := scanChannel(row)
	return ch, err == nil
}

func (h *Handler) channelAuthorName(ctx context.Context, userID string) string {
	user, err := h.Queries.GetUser(ctx, parseUUID(userID))
	if err == nil && strings.TrimSpace(user.Name) != "" {
		return user.Name
	}
	if err == nil && strings.TrimSpace(user.Email) != "" {
		return user.Email
	}
	return "You"
}

func (h *Handler) agentName(ctx context.Context, agentID pgtype.UUID) string {
	agent, err := h.Queries.GetAgent(ctx, agentID)
	if err == nil && strings.TrimSpace(agent.Name) != "" {
		return agent.Name
	}
	return "Agent"
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanChannel(row rowScanner) (ChannelResponse, error) {
	var id, wsID, createdBy pgtype.UUID
	var name string
	var desc, lark pgtype.Text
	var createdAt, updatedAt pgtype.Timestamptz
	var kind string
	if err := row.Scan(&id, &wsID, &name, &desc, &lark, &createdBy, &createdAt, &updatedAt, &kind); err != nil {
		return ChannelResponse{}, err
	}
	return ChannelResponse{ID: uuidToString(id), WorkspaceID: uuidToString(wsID), Name: name, Kind: kind, Description: textToPtr(desc), LarkChatID: textToPtr(lark), CreatedBy: uuidToString(createdBy), CreatedAt: timestampToString(createdAt), UpdatedAt: timestampToString(updatedAt)}, nil
}

func scanChannelMessage(row rowScanner) (ChannelMessageResponse, error) {
	var id, channelID, wsID, authorID pgtype.UUID
	var authorType, authorName, content, source string
	var external, thread pgtype.Text
	var triggerDepth int
	var createdAt pgtype.Timestamptz
	if err := row.Scan(&id, &channelID, &wsID, &authorType, &authorID, &authorName, &content, &source, &external, &thread, &triggerDepth, &createdAt); err != nil {
		return ChannelMessageResponse{}, err
	}
	return ChannelMessageResponse{ID: uuidToString(id), ChannelID: uuidToString(channelID), WorkspaceID: uuidToString(wsID), AuthorType: authorType, AuthorID: uuidToPtr(authorID), AuthorName: authorName, Content: content, Source: source, ExternalMessageID: textToPtr(external), ThreadID: textToPtr(thread), TriggerDepth: triggerDepth, CreatedAt: timestampToString(createdAt)}, nil
}

func trimTextPtr(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func nullableUUID(u pgtype.UUID) any {
	if !u.Valid {
		return nil
	}
	return u
}

func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func errorsIsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
