package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const channelNameMaxLen = 80
const channelMessageMaxLen = 20000
const channelContextMessageLimit = 12
const channelRunTriggerLimit = 10
const channelUserTypingExpiresInMS = 5000
const channelAgentTypingExpiresInMS = 10 * 60 * 1000
const channelMessagesDefaultLimit = 50
const channelMessagesMaxLimit = 100
const channelThreadDefaultLimit = 50
const channelThreadMaxLimit = 100
const channelClientMessageIDMaxLen = 128
const channelOutputContractInstruction = "Channel output contract: use the task-scoped Multica CLI transport for visible chat output. Run `multica send --message ...` for a text reply to the current channel/thread, `multica send --target \"#channel\"|\"#channel:<message-id>\"|\"dm:@handle\" --message ...` for an explicit target, and `multica react --message-id <id> --emoji ...` for reactions. You may read context with `multica message read` and search with `multica message search`. After a successful send or react, do not repeat the same content in final assistant output; finish without extra visible text. Do not print JSON envelopes, action objects, no_reply/stay_silent tokens, tool intent, analysis, or described commands as the final answer."
const channelDirectedReplyInstruction = "This run is directly addressed to you. You must produce a visible result by running `multica send` or, when a reaction is explicitly requested and sufficient, `multica react`. Answer helpfully, ask a follow-up question, or acknowledge the request in words. Do not return no_reply, stay_silent, JSON, or any other silent/protocol outcome for a direct mention, direct question, assigned task, or DM-style continuation."
const channelAmbientNoReplyInstruction = "If you should not reply, finish without a visible reply. Do not run `multica send`/`multica react`, and do not print no_reply, stay_silent, JSON, or CLI/protocol text."
const channelStickerReplyInstruction = "Sticker replies: structured sticker parts are unavailable in the CLI transport. If the user explicitly asks for a sticker/表情包, or a sticker-only social reply would otherwise be natural, use `multica send` with a short plain-text reply instead and do not output sticker JSON or :sticker:<id>: tokens."
const channelNameTakenCode = "channel_name_taken"
const channelNameUniqueConstraint = "channel_workspace_id_name_key"

var errChannelClientMessageConflict = errors.New("client_message_id already used for different channel message payload")

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
	ArchivedAt  *string `json:"archived_at,omitempty"`
	ArchivedBy  *string `json:"archived_by,omitempty"`
	// List-only enrichments (zero/omitted on create/update/get responses).
	UnreadCount     int                  `json:"unread_count"`
	RealUnreadCount int                  `json:"real_unread_count"`
	ManuallyUnread  bool                 `json:"manually_unread,omitempty"`
	PinnedAt        *string              `json:"pinned_at,omitempty"`
	MutedAt         *string              `json:"muted_at,omitempty"`
	Muted           bool                 `json:"muted,omitempty"`
	LastMessage     *ChannelLastMessage  `json:"last_message,omitempty"`
	Members         []ChannelMemberBrief `json:"members,omitempty"`
}

type ChannelLastMessage struct {
	Type       string                 `json:"type"`
	AuthorName string                 `json:"author_name"`
	Content    string                 `json:"content"`
	Parts      []protocol.MessagePart `json:"parts,omitempty"`
	CreatedAt  string                 `json:"created_at"`
}

type ChannelMemberBrief struct {
	MemberType  string `json:"member_type"`
	MemberID    string `json:"member_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type ChannelMemberResponse struct {
	MemberType  string `json:"member_type"`
	MemberID    string `json:"member_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type ChannelMessageResponse struct {
	ID                    string                        `json:"id"`
	ChannelID             string                        `json:"channel_id"`
	WorkspaceID           string                        `json:"workspace_id"`
	Seq                   int64                         `json:"seq"`
	Type                  string                        `json:"type"`
	AuthorID              *string                       `json:"author_id"`
	AuthorName            string                        `json:"author_name"`
	Content               string                        `json:"content"`
	Parts                 []protocol.MessagePart        `json:"parts,omitempty"`
	Source                string                        `json:"source"`
	ExternalMessageID     *string                       `json:"external_message_id"`
	ClientMessageID       *string                       `json:"client_message_id"`
	ReplyToMessageID      *string                       `json:"reply_to_message_id,omitempty"`
	ReplyTo               *ChannelMessageReply          `json:"reply_to,omitempty"`
	ThreadRootMessageID   *string                       `json:"thread_root_message_id,omitempty"`
	ThreadRoot            *ChannelMessageReply          `json:"thread_root,omitempty"`
	ThreadReplyCount      int                           `json:"thread_reply_count,omitempty"`
	ThreadLastReplyAt     *string                       `json:"thread_last_reply_at,omitempty"`
	ThreadUnreadCount     int                           `json:"thread_unread_count,omitempty"`
	ThreadFollowed        bool                          `json:"thread_followed,omitempty"`
	ThreadParticipants    []ChannelThreadParticipant    `json:"thread_participants,omitempty"`
	ThreadWakeAnnotations []ChannelThreadWakeAnnotation `json:"thread_wake_annotations,omitempty"`
	ThreadID              *string                       `json:"thread_id,omitempty"`
	TriggerDepth          int                           `json:"trigger_depth"`
	Reactions             []ChannelReactionResponse     `json:"reactions,omitempty"`
	CreatedAt             string                        `json:"created_at"`
	EditedAt              *string                       `json:"edited_at,omitempty"`
	DeletedAt             *string                       `json:"deleted_at,omitempty"`
	// Attachments linked to this message via channel_message_id. The chat
	// bubble renders file/image cards from these.
	Attachments []AttachmentResponse `json:"attachments,omitempty"`
}

type ChannelThreadParticipant struct {
	Key         string `json:"key"`
	MemberType  string `json:"member_type"`
	MemberID    string `json:"member_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Followed    bool   `json:"followed"`
}

type ChannelThreadWakeAnnotation struct {
	Key         string  `json:"key"`
	MemberType  string  `json:"member_type"`
	MemberID    string  `json:"member_id"`
	DisplayName string  `json:"display_name"`
	State       string  `json:"state"`
	Reason      *string `json:"reason,omitempty"`
}

type ChannelMessagesCursorResponse struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
	Seq       int64  `json:"seq"`
}

type ChannelMessagesPageResponse struct {
	Messages   []ChannelMessageResponse       `json:"messages"`
	Limit      int                            `json:"limit"`
	HasMore    bool                           `json:"has_more"`
	NextCursor *ChannelMessagesCursorResponse `json:"next_cursor,omitempty"`
}

type ChannelThreadMessagesCursorResponse struct {
	BeforeSeq int64  `json:"before_seq,omitempty"`
	Before    string `json:"before"`
	BeforeID  string `json:"before_id"`
}

type ChannelThreadMessagesPageResponse struct {
	Messages   []ChannelMessageResponse             `json:"messages"`
	NextCursor *ChannelThreadMessagesCursorResponse `json:"next_cursor"`
}

type ChannelMessageReply struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	AuthorID   *string                `json:"author_id"`
	AuthorName string                 `json:"author_name"`
	Content    string                 `json:"content"`
	Parts      []protocol.MessagePart `json:"parts,omitempty"`
	CreatedAt  string                 `json:"created_at"`
}

type ChannelReactionResponse struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	Emoji     string `json:"emoji"`
	CreatedAt string `json:"created_at"`
}

type ChannelMessageSearchResponse struct {
	Query   string                       `json:"query"`
	Total   int                          `json:"total"`
	Results []ChannelMessageSearchResult `json:"results"`
}

type ChannelMessageSearchResult struct {
	MessageID           string  `json:"message_id"`
	ChannelID           string  `json:"channel_id"`
	ThreadRootMessageID *string `json:"thread_root_message_id,omitempty"`
	Type                string  `json:"type"`
	AuthorID            *string `json:"author_id"`
	AuthorName          string  `json:"author_name"`
	Content             string  `json:"content"`
	CreatedAt           string  `json:"created_at"`
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
	Content          string                 `json:"content"`
	Parts            []protocol.MessagePart `json:"parts"`
	AttachmentIDs    []string               `json:"attachment_ids"`
	ReplyToMessageID *string                `json:"reply_to_message_id"`
	ClientMessageID  *string                `json:"client_message_id"`
	ShowInChannel    *bool                  `json:"show_in_channel,omitempty"`
	// Legacy #252 name accepted only as a transition fallback. New clients use show_in_channel.
	AlsoSendToChannel *bool `json:"also_send_to_channel,omitempty"`
}

type UpdateChannelMessageRequest struct {
	Content string                 `json:"content"`
	Parts   []protocol.MessagePart `json:"parts"`
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
	archivedOnly := queryBool(r, "archived")
	rows, err := h.DB.Query(r.Context(), `
		SELECT ch.id, ch.workspace_id, ch.name, ch.description, ch.lark_chat_id, ch.created_by, ch.created_at, ch.updated_at, ch.kind,
		       ch.archived_at, ch.archived_by, cm.pinned_at, cm.manual_unread_at, COALESCE(vcm.muted_at, cm.muted_at),
		       lm.author_type, lm.author_name, lm.content, lm.parts, lm.created_at,
		       COALESCE(uc.cnt, 0),
		       GREATEST(COALESCE(uc.cnt, 0), CASE WHEN cm.manual_unread_at IS NOT NULL THEN 1 ELSE 0 END)
		FROM channel ch
		JOIN channel_member cm ON cm.channel_id = ch.id AND cm.member_type = 'user' AND cm.member_id = $2
		JOIN conversation conv ON conv.channel_id = ch.id
		LEFT JOIN conversation_member vcm
		  ON vcm.conversation_id = conv.id
		 AND vcm.member_type = 'user'
		 AND vcm.member_id = $2
		LEFT JOIN LATERAL (
			SELECT author_type, author_name, content, parts, created_at
			FROM channel_message m
			WHERE m.channel_id = ch.id
			  AND (m.thread_root_message_id IS NULL OR m.main_timeline_visible)
			  AND m.deleted_at IS NULL
			ORDER BY m.seq DESC LIMIT 1
		) lm ON true
		LEFT JOIN channel_read cr ON cr.channel_id = ch.id AND cr.user_id = $2
		LEFT JOIN LATERAL (
			SELECT count(*) AS cnt FROM channel_message m
			WHERE m.channel_id = ch.id
			  AND m.seq > COALESCE(vcm.last_read_seq, cr.last_read_seq, 0)
			  AND NOT (m.author_type = 'user' AND m.author_id = $2)
			  AND (m.thread_root_message_id IS NULL OR m.main_timeline_visible)
			  AND m.deleted_at IS NULL
		) uc ON true
		WHERE ch.workspace_id = $1 AND ch.kind = 'group'
		  AND (($3 AND ch.archived_at IS NOT NULL) OR (NOT $3 AND ch.archived_at IS NULL))
		ORDER BY CASE WHEN $3 THEN ch.archived_at ELSE cm.pinned_at END DESC NULLS LAST, ch.updated_at DESC, ch.created_at DESC`, parseUUID(workspaceID), uid, archivedOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	defer rows.Close()
	out := []ChannelResponse{}
	channelIDs := []pgtype.UUID{}
	for rows.Next() {
		var id, wsID, createdBy, archivedBy pgtype.UUID
		var name string
		var desc, lark, lastType, lastName, lastContent pgtype.Text
		var lastParts []byte
		var createdAt, updatedAt, archivedAt, pinnedAt, manualUnreadAt, mutedAt, lastAt pgtype.Timestamptz
		var realUnread, unread int
		var kind string
		if err := rows.Scan(&id, &wsID, &name, &desc, &lark, &createdBy, &createdAt, &updatedAt, &kind,
			&archivedAt, &archivedBy, &pinnedAt, &manualUnreadAt, &mutedAt, &lastType, &lastName, &lastContent, &lastParts, &lastAt, &realUnread, &unread); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channels")
			return
		}
		ch := ChannelResponse{
			ID: uuidToString(id), WorkspaceID: uuidToString(wsID), Name: name,
			Description: textToPtr(desc), LarkChatID: textToPtr(lark), CreatedBy: uuidToString(createdBy),
			CreatedAt: timestampToString(createdAt), UpdatedAt: timestampToString(updatedAt),
			ArchivedAt: timestampToPtr(archivedAt), ArchivedBy: uuidToPtr(archivedBy),
			Kind: kind, UnreadCount: unread, RealUnreadCount: realUnread, ManuallyUnread: manualUnreadAt.Valid,
			PinnedAt: timestampToPtr(pinnedAt), MutedAt: timestampToPtr(mutedAt), Muted: mutedAt.Valid, Members: []ChannelMemberBrief{},
		}
		if lastContent.Valid {
			ch.LastMessage = channelLastMessage(lastType.String, lastName.String, lastContent.String, lastParts, lastAt)
		}
		out = append(out, ch)
		channelIDs = append(channelIDs, id)
	}
	rows.Close()

	// Second pass: members for the avatar stack, grouped by channel.
	if len(channelIDs) > 0 {
		memberRows, err := h.DB.Query(r.Context(), `
			SELECT cm.channel_id, cm.member_type, cm.member_id,
			       COALESCE(u.name, a.name, ''),
			       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, '')
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
				var memberType, memberName, memberDisplayName string
				if err := memberRows.Scan(&chID, &memberType, &memberID, &memberName, &memberDisplayName); err != nil {
					continue
				}
				key := uuidToString(chID)
				grouped[key] = append(grouped[key], ChannelMemberBrief{MemberType: memberType, MemberID: uuidToString(memberID), Name: memberName, DisplayName: firstNonEmpty(memberDisplayName, memberName)})
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
		WITH conv AS (
		  SELECT id, workspace_id, last_seq
		  FROM conversation
		  WHERE channel_id = $1
		),
		read_state AS (
		  INSERT INTO channel_read (channel_id, user_id, last_read_at, last_read_seq)
		  SELECT $1, $2, now(), conv.last_seq FROM conv
		  ON CONFLICT (channel_id, user_id)
		  DO UPDATE SET last_read_at = now(), last_read_seq = EXCLUDED.last_read_seq
		  RETURNING channel_id, user_id, last_read_seq
		)
		INSERT INTO conversation_member (conversation_id, workspace_id, member_type, member_id, last_read_seq, followed_at, updated_at)
		SELECT conv.id, conv.workspace_id, 'user', $2, conv.last_seq, now(), now()
		FROM conv
		ON CONFLICT (conversation_id, member_type, member_id)
		DO UPDATE SET last_read_seq = EXCLUDED.last_read_seq, updated_at = now()`, channelID, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark channel read")
		return
	}
	_, _ = h.DB.Exec(r.Context(), `
		UPDATE channel_member
		SET manual_unread_at = NULL
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`,
		channelID, parseUUID(workspaceID), parseUUID(userID))
	h.clearDMPeerManualUnreadForChannel(r.Context(), workspaceID, userID, channelID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) PinChannel(w http.ResponseWriter, r *http.Request) {
	h.setChannelPinned(w, r, true)
}

func (h *Handler) UnpinChannel(w http.ResponseWriter, r *http.Request) {
	h.setChannelPinned(w, r, false)
}

func (h *Handler) MuteChannel(w http.ResponseWriter, r *http.Request) {
	h.setChannelMuted(w, r, true)
}

func (h *Handler) UnmuteChannel(w http.ResponseWriter, r *http.Request) {
	h.setChannelMuted(w, r, false)
}

func (h *Handler) setChannelPinned(w http.ResponseWriter, r *http.Request, pinned bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, userUUID) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	value := "now()"
	if !pinned {
		value = "NULL"
	}
	if _, err := h.DB.Exec(r.Context(), fmt.Sprintf(`
		UPDATE channel_member
		SET pinned_at = %s
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`, value),
		channelID, parseUUID(workspaceID), userUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel pin")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) setChannelMuted(w http.ResponseWriter, r *http.Request, muted bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, userUUID) {
		return
	}
	if _, err := h.DB.Exec(r.Context(), `
		WITH updated_channel_member AS (
		  UPDATE channel_member
		  SET muted_at = CASE WHEN $4 THEN now() ELSE NULL END
		  WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3
		  RETURNING channel_id, workspace_id, member_id, muted_at
		),
		conv AS (
		  SELECT id
		  FROM conversation
		  WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET muted_at = updated_channel_member.muted_at,
		    updated_at = now()
		FROM updated_channel_member, conv
		WHERE cm.conversation_id = conv.id
		  AND cm.workspace_id = updated_channel_member.workspace_id
		  AND cm.member_type = 'user'
		  AND cm.member_id = updated_channel_member.member_id`,
		channelID, parseUUID(workspaceID), userUUID, muted); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel mute")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) MarkChannelUnread(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, userUUID) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if _, err := h.DB.Exec(r.Context(), `
		UPDATE channel_member
		SET manual_unread_at = now()
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`,
		channelID, parseUUID(workspaceID), userUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark channel unread")
		return
	}
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
		RETURNING id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind, archived_at, archived_by`,
		parseUUID(workspaceID), name, desc, larkChatID, parseUUID(userID))
	ch, err := scanChannel(row)
	if err != nil {
		if isChannelNameTakenError(err) {
			writeCodedError(w, http.StatusConflict, channelNameTakenCode, "channel name already exists")
			return
		}
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

func isChannelNameTakenError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == channelNameUniqueConstraint
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
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
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
		RETURNING id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind, archived_at, archived_by`,
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
	if !h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if _, ok := h.archiveChannel(w, r, workspaceID, channelID, parseUUID(userID)); !ok {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ArchiveChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	ch, ok := h.archiveChannel(w, r, workspaceID, channelID, parseUUID(userID))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *Handler) RestoreChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	row := h.DB.QueryRow(r.Context(), `
		UPDATE channel
		SET archived_at = NULL, archived_by = NULL, updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND kind = 'group'
		RETURNING id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind, archived_at, archived_by`,
		channelID, parseUUID(workspaceID))
	ch, err := scanChannel(row)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to restore channel")
		return
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, ch)
	writeJSON(w, http.StatusOK, ch)
}

func (h *Handler) archiveChannel(w http.ResponseWriter, r *http.Request, workspaceID string, channelID, userID pgtype.UUID) (ChannelResponse, bool) {
	row := h.DB.QueryRow(r.Context(), `
		UPDATE channel
		SET archived_at = COALESCE(archived_at, now()), archived_by = COALESCE(archived_by, $3), updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND kind = 'group'
		RETURNING id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind, archived_at, archived_by`,
		channelID, parseUUID(workspaceID), userID)
	ch, err := scanChannel(row)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "channel not found")
			return ChannelResponse{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to archive channel")
		return ChannelResponse{}, false
	}
	h.publish(protocol.EventChannelDeleted, workspaceID, "member", uuidToString(userID), map[string]any{"id": uuidToString(channelID)})
	return ch, true
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
		       COALESCE(u.name, a.name, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, ''),
		       cm.created_at
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
		var typ, name, displayName string
		var id pgtype.UUID
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&typ, &id, &name, &displayName, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel members")
			return
		}
		out = append(out, ChannelMemberResponse{MemberType: typ, MemberID: uuidToString(id), Name: name, DisplayName: firstNonEmpty(displayName, name), CreatedAt: timestampToString(createdAt)})
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
	if !h.requireGroupChannel(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
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
	// When a NEW human joins, every agent member greets them briefly.
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
	if !h.requireGroupChannel(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !(memberType == "user" && uuidToString(memberID) == userID) {
		if !h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
			return
		}
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
	ch, found := h.getChannel(r.Context(), workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !h.requireDMChannelAgentAccess(w, r, workspaceID, userID, ch) {
		return
	}
	limit, beforeSeq, beforeCreatedAt, beforeID, err := parseChannelMessagesPageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := h.DB.Query(r.Context(), `
			SELECT m.id, m.channel_id, m.workspace_id, m.author_type, m.author_id, m.author_name, m.content, m.parts, m.source, m.external_message_id, m.client_message_id, m.reply_to_message_id, m.thread_root_message_id, m.thread_id, m.trigger_depth, m.seq, m.created_at, m.edited_at, m.deleted_at
		FROM channel_message m
		WHERE m.channel_id = $1 AND m.workspace_id = $2
		  AND (m.thread_root_message_id IS NULL OR m.main_timeline_visible)
		  AND (
		    m.deleted_at IS NULL
		    OR EXISTS (
		      SELECT 1
		      FROM channel_message replies
		      WHERE replies.workspace_id = m.workspace_id
		        AND replies.channel_id = m.channel_id
		        AND replies.thread_root_message_id = m.id
		        AND replies.deleted_at IS NULL
		    )
		  )
		  AND (
		    ($3::bigint = 0 AND $4::timestamptz IS NULL AND $5::uuid IS NULL)
		    OR ($3::bigint > 0 AND m.seq < $3::bigint)
		    OR ($3::bigint = 0 AND (m.created_at, m.id) < ($4::timestamptz, $5::uuid))
		  )
		ORDER BY m.seq DESC
		LIMIT $6`, channelID, parseUUID(workspaceID), beforeSeq, beforeCreatedAt, beforeID, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel messages")
		return
	}
	defer rows.Close()
	desc := []ChannelMessageResponse{}
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel messages")
			return
		}
		desc = append(desc, msg)
	}
	hasMore := len(desc) > limit
	if hasMore {
		desc = desc[:limit]
	}
	var nextCursor *ChannelMessagesCursorResponse
	if hasMore && len(desc) > 0 {
		oldest := desc[len(desc)-1]
		nextCursor = &ChannelMessagesCursorResponse{
			CreatedAt: oldest.CreatedAt,
			ID:        oldest.ID,
			Seq:       oldest.Seq,
		}
	}
	out := make([]ChannelMessageResponse, 0, len(desc))
	for i := len(desc) - 1; i >= 0; i-- {
		out = append(out, desc[i])
	}
	messageIDs := make([]pgtype.UUID, len(out))
	for i, m := range out {
		messageIDs[i] = parseUUID(m.ID)
	}
	grouped := h.groupChannelMessageAttachments(r.Context(), workspaceID, messageIDs)
	for i := range out {
		out[i].Attachments = grouped[out[i].ID]
	}
	h.attachChannelMessageReactions(r.Context(), workspaceID, out)
	h.attachChannelMessageReplySummaries(r.Context(), workspaceID, out)
	h.attachChannelMessageThreadRootSummaries(r.Context(), workspaceID, out)
	h.attachChannelMessageThreadMetadata(r.Context(), workspaceID, parseUUID(userID), out)
	h.attachChannelMessageThreadReadModel(r.Context(), workspaceID, out)
	applyChannelMessageTombstoneReadModel(out)
	writeJSON(w, http.StatusOK, ChannelMessagesPageResponse{
		Messages:   out,
		Limit:      limit,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

func (h *Handler) SearchChannelMessages(w http.ResponseWriter, r *http.Request) {
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
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	resp := ChannelMessageSearchResponse{Query: query, Results: []ChannelMessageSearchResult{}}
	if query == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	limit := boundedQueryInt(r, "limit", 50, 100)
	pattern := "%" + escapeLike(query) + "%"
	if err := h.DB.QueryRow(r.Context(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2 AND author_type <> 'system' AND deleted_at IS NULL AND content ILIKE $3 ESCAPE '\'`,
		channelID, parseUUID(workspaceID), pattern).Scan(&resp.Total); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search channel messages")
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, channel_id, thread_root_message_id, author_type, author_id, author_name, content, created_at
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2 AND author_type <> 'system' AND deleted_at IS NULL AND content ILIKE $3 ESCAPE '\'
		ORDER BY seq ASC
		LIMIT $4`, channelID, parseUUID(workspaceID), pattern, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search channel messages")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, chID, threadRootID, authorID pgtype.UUID
		var authorType, authorName, content string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &chID, &threadRootID, &authorType, &authorID, &authorName, &content, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel message search results")
			return
		}
		resp.Results = append(resp.Results, ChannelMessageSearchResult{
			MessageID:           uuidToString(id),
			ChannelID:           uuidToString(chID),
			ThreadRootMessageID: uuidToPtr(threadRootID),
			Type:                authorType,
			AuthorID:            uuidToPtr(authorID),
			AuthorName:          authorName,
			Content:             content,
			CreatedAt:           timestampToString(createdAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) validateChannelReplyTarget(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID, raw *string) (pgtype.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return pgtype.UUID{}, true
	}
	replyToID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*raw), "reply_to_message_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	var exists bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_message
			WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND author_type <> 'system'
		)`, replyToID, channelID, parseUUID(workspaceID)).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate reply target")
		return pgtype.UUID{}, false
	}
	if !exists {
		writeError(w, http.StatusBadRequest, "reply_to_message_id must reference a message in this channel")
		return pgtype.UUID{}, false
	}
	return replyToID, true
}

func (h *Handler) attachChannelMessageReplySummary(ctx context.Context, workspaceID string, msg ChannelMessageResponse) ChannelMessageResponse {
	messages := []ChannelMessageResponse{msg}
	h.attachChannelMessageReplySummaries(ctx, workspaceID, messages)
	h.attachChannelMessageReactions(ctx, workspaceID, messages)
	return messages[0]
}

func (h *Handler) attachSingleChannelMessageDetails(ctx context.Context, workspaceID string, userID pgtype.UUID, msg ChannelMessageResponse) ChannelMessageResponse {
	messages := []ChannelMessageResponse{msg}
	h.attachChannelMessageReplySummaries(ctx, workspaceID, messages)
	h.attachChannelMessageReactions(ctx, workspaceID, messages)
	h.attachChannelMessageThreadMetadata(ctx, workspaceID, userID, messages)
	h.attachChannelMessageThreadReadModel(ctx, workspaceID, messages)
	msg = messages[0]
	msg.Attachments = h.groupChannelMessageAttachments(ctx, workspaceID, []pgtype.UUID{parseUUID(msg.ID)})[msg.ID]
	applyChannelMessageTombstone(&msg)
	return msg
}

func applyChannelMessageTombstoneReadModel(messages []ChannelMessageResponse) {
	for i := range messages {
		applyChannelMessageTombstone(&messages[i])
	}
}

func applyChannelMessageTombstone(msg *ChannelMessageResponse) {
	if msg == nil || msg.DeletedAt == nil {
		return
	}
	msg.Content = ""
	msg.Parts = nil
	msg.Attachments = nil
	msg.Reactions = nil
	msg.ReplyTo = nil
	msg.ThreadRoot = nil
	msg.ThreadParticipants = nil
	msg.ThreadWakeAnnotations = nil
}

func (h *Handler) attachChannelMessageReactions(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	messageIDs := []pgtype.UUID{}
	channelIDs := map[string]string{}
	for _, msg := range messages {
		if msg.DeletedAt != nil {
			continue
		}
		messageIDs = append(messageIDs, parseUUID(msg.ID))
		channelIDs[msg.ID] = msg.ChannelID
	}
	if len(messageIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, channel_message_id, actor_type, actor_id, emoji, created_at
		FROM channel_message_reaction
		WHERE workspace_id = $1 AND channel_message_id = ANY($2::uuid[])
		ORDER BY created_at ASC`, parseUUID(workspaceID), messageIDs)
	if err != nil {
		slog.Warn("channel reactions: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	byMessage := map[string][]ChannelReactionResponse{}
	for rows.Next() {
		var id, messageID, actorID pgtype.UUID
		var actorType, emoji string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &messageID, &actorType, &actorID, &emoji, &createdAt); err != nil {
			continue
		}
		key := uuidToString(messageID)
		byMessage[key] = append(byMessage[key], ChannelReactionResponse{
			ID:        uuidToString(id),
			ChannelID: channelIDs[key],
			MessageID: key,
			ActorType: actorType,
			ActorID:   uuidToString(actorID),
			Emoji:     emoji,
			CreatedAt: timestampToString(createdAt),
		})
	}
	for i := range messages {
		if messages[i].DeletedAt != nil {
			messages[i].Reactions = nil
			continue
		}
		messages[i].Reactions = byMessage[messages[i].ID]
	}
}

func (h *Handler) attachChannelMessageReplySummaries(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	replyIDs := []pgtype.UUID{}
	seen := map[string]bool{}
	for _, msg := range messages {
		if msg.DeletedAt != nil {
			continue
		}
		if msg.ReplyToMessageID == nil || seen[*msg.ReplyToMessageID] {
			continue
		}
		seen[*msg.ReplyToMessageID] = true
		replyIDs = append(replyIDs, parseUUID(*msg.ReplyToMessageID))
	}
	if len(replyIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, author_type, author_id, author_name, content, parts, created_at
		FROM channel_message
		WHERE workspace_id = $1 AND id = ANY($2::uuid[]) AND deleted_at IS NULL`,
		parseUUID(workspaceID), replyIDs)
	if err != nil {
		slog.Warn("channel reply summary: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	byID := map[string]ChannelMessageReply{}
	for rows.Next() {
		var id, authorID pgtype.UUID
		var authorType, authorName, content string
		var parts []byte
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &authorType, &authorID, &authorName, &content, &parts, &createdAt); err != nil {
			continue
		}
		key := uuidToString(id)
		byID[key] = ChannelMessageReply{
			ID:         key,
			Type:       authorType,
			AuthorID:   uuidToPtr(authorID),
			AuthorName: authorName,
			Content:    content,
			Parts:      messageparts.Decode(parts),
			CreatedAt:  timestampToString(createdAt),
		}
	}
	for i := range messages {
		if messages[i].ReplyToMessageID == nil {
			continue
		}
		if reply, ok := byID[*messages[i].ReplyToMessageID]; ok {
			messages[i].ReplyTo = &reply
		}
	}
}

func (h *Handler) attachChannelMessageThreadRootSummaries(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	rootIDs := []pgtype.UUID{}
	seen := map[string]bool{}
	for _, msg := range messages {
		if msg.DeletedAt != nil {
			continue
		}
		if msg.ThreadRootMessageID == nil || seen[*msg.ThreadRootMessageID] {
			continue
		}
		seen[*msg.ThreadRootMessageID] = true
		rootIDs = append(rootIDs, parseUUID(*msg.ThreadRootMessageID))
	}
	if len(rootIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, author_type, author_id, author_name, content, parts, created_at
		FROM channel_message
		WHERE workspace_id = $1 AND id = ANY($2::uuid[]) AND deleted_at IS NULL`,
		parseUUID(workspaceID), rootIDs)
	if err != nil {
		slog.Warn("channel thread root summary: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	byID := map[string]ChannelMessageReply{}
	for rows.Next() {
		var id, authorID pgtype.UUID
		var authorType, authorName, content string
		var parts []byte
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &authorType, &authorID, &authorName, &content, &parts, &createdAt); err != nil {
			continue
		}
		key := uuidToString(id)
		byID[key] = ChannelMessageReply{
			ID:         key,
			Type:       authorType,
			AuthorID:   uuidToPtr(authorID),
			AuthorName: authorName,
			Content:    content,
			Parts:      messageparts.Decode(parts),
			CreatedAt:  timestampToString(createdAt),
		}
	}
	for i := range messages {
		if messages[i].ThreadRootMessageID == nil {
			continue
		}
		if root, ok := byID[*messages[i].ThreadRootMessageID]; ok {
			messages[i].ThreadRoot = &root
		}
	}
}

func (h *Handler) attachChannelMessageThreadMetadata(ctx context.Context, workspaceID string, userID pgtype.UUID, messages []ChannelMessageResponse) {
	rootIDs := []pgtype.UUID{}
	for _, msg := range messages {
		if msg.ThreadRootMessageID != nil {
			continue
		}
		rootIDs = append(rootIDs, parseUUID(msg.ID))
	}
	if len(rootIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT roots.id,
		       count(replies.id)::int AS reply_count,
		       max(replies.created_at) AS last_reply_at,
		       COALESCE(tp.followed_at IS NOT NULL, false) AS followed,
	       CASE
	         WHEN tp.followed_at IS NOT NULL THEN count(replies.id) FILTER (
	           WHERE replies.seq > COALESCE(tp.last_read_seq, 0)
	             AND NOT (replies.author_type = 'user' AND replies.author_id = $3)
	             AND NOT replies.main_timeline_visible
	         )::int
	         ELSE 0
	       END AS unread_count
		FROM channel_message roots
		LEFT JOIN channel_message replies ON replies.channel_id = roots.channel_id
			AND replies.thread_root_message_id = roots.id
			AND replies.deleted_at IS NULL
		LEFT JOIN thread_participant tp
		  ON tp.root_message_id = roots.id
		 AND tp.member_type = 'user'
		 AND tp.member_id = $3
		WHERE roots.workspace_id = $2 AND roots.id = ANY($1::uuid[])
		GROUP BY roots.id, tp.followed_at, tp.last_read_seq`,
		rootIDs, parseUUID(workspaceID), userID)
	if err != nil {
		slog.Warn("channel thread metadata: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	type threadMeta struct {
		replyCount  int
		lastReplyAt *string
		followed    bool
		unreadCount int
	}
	byID := map[string]threadMeta{}
	for rows.Next() {
		var id pgtype.UUID
		var count, unread int
		var lastReplyAt pgtype.Timestamptz
		var followed bool
		if err := rows.Scan(&id, &count, &lastReplyAt, &followed, &unread); err != nil {
			continue
		}
		byID[uuidToString(id)] = threadMeta{
			replyCount:  count,
			lastReplyAt: timestampToPtr(lastReplyAt),
			followed:    followed,
			unreadCount: unread,
		}
	}
	for i := range messages {
		meta, ok := byID[messages[i].ID]
		if !ok {
			continue
		}
		messages[i].ThreadReplyCount = meta.replyCount
		messages[i].ThreadLastReplyAt = meta.lastReplyAt
		messages[i].ThreadFollowed = meta.followed
		messages[i].ThreadUnreadCount = meta.unreadCount
	}
}

func (h *Handler) attachChannelMessageThreadReadModel(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	rootIDs := []pgtype.UUID{}
	for _, msg := range messages {
		if msg.ThreadRootMessageID != nil {
			continue
		}
		rootIDs = append(rootIDs, parseUUID(msg.ID))
	}
	if len(rootIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		WITH latest_task AS (
		  SELECT DISTINCT ON (cm.channel_thread_root_message_id, atq.agent_id)
		         cm.channel_thread_root_message_id AS root_message_id,
		         atq.agent_id,
		         atq.status,
		         COALESCE(atq.result::text, '{}') AS result,
		         cm.created_at AS prompt_created_at
		  FROM chat_message cm
		  JOIN agent_task_queue atq ON atq.id = cm.task_id
		  WHERE cm.channel_thread_root_message_id = ANY($1::uuid[])
		    AND cm.role = 'user'
		    AND cm.task_id IS NOT NULL
		  ORDER BY cm.channel_thread_root_message_id, atq.agent_id, atq.created_at DESC, atq.id DESC
		),
		latest_agent_reply AS (
		  SELECT DISTINCT ON (thread_root_message_id, author_id)
		         thread_root_message_id AS root_message_id,
		         author_id AS agent_id
		  FROM channel_message reply
		  LEFT JOIN latest_task lt
		    ON lt.root_message_id = reply.thread_root_message_id
		   AND lt.agent_id = reply.author_id
		  WHERE reply.workspace_id = $2
		    AND reply.thread_root_message_id = ANY($1::uuid[])
		    AND reply.author_type = 'agent'
		    AND reply.deleted_at IS NULL
		    AND (lt.prompt_created_at IS NULL OR reply.created_at > lt.prompt_created_at)
		  ORDER BY reply.thread_root_message_id, reply.author_id, reply.seq DESC
		)
		SELECT tp.root_message_id,
		       tp.member_type,
		       tp.member_id,
		       COALESCE(u.name, a.name, '') AS name,
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, '') AS display_name,
		       COALESCE(tp.followed_at IS NOT NULL, false) AS followed,
		       COALESCE(lt.status, '') AS task_status,
		       COALESCE(lt.result, '{}') AS task_result,
		       (lar.agent_id IS NOT NULL) AS has_agent_reply
		FROM thread_participant tp
		LEFT JOIN "user" u ON tp.member_type = 'user' AND u.id = tp.member_id
		LEFT JOIN agent a ON tp.member_type = 'agent' AND a.id = tp.member_id
		LEFT JOIN latest_task lt
		  ON tp.member_type = 'agent'
		 AND lt.root_message_id = tp.root_message_id
		 AND lt.agent_id = tp.member_id
		LEFT JOIN latest_agent_reply lar
		  ON tp.member_type = 'agent'
		 AND lar.root_message_id = tp.root_message_id
		 AND lar.agent_id = tp.member_id
		WHERE tp.root_message_id = ANY($1::uuid[])
		  AND tp.wake_state <> 'removed'
		ORDER BY tp.root_message_id, tp.created_at ASC`,
		rootIDs, parseUUID(workspaceID))
	if err != nil {
		slog.Warn("channel thread read-model: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()

	participantsByRoot := map[string][]ChannelThreadParticipant{}
	wakeByRoot := map[string][]ChannelThreadWakeAnnotation{}
	for rows.Next() {
		var rootID, memberID pgtype.UUID
		var memberType, name, displayName string
		var followed bool
		var taskStatus, taskResult string
		var hasAgentReply bool
		if err := rows.Scan(&rootID, &memberType, &memberID, &name, &displayName, &followed, &taskStatus, &taskResult, &hasAgentReply); err != nil {
			continue
		}
		memberIDText := uuidToString(memberID)
		key := channelThreadParticipantKey(memberType, memberIDText)
		rootKey := uuidToString(rootID)
		displayName = firstNonEmpty(displayName, name, memberIDText)
		participantsByRoot[rootKey] = append(participantsByRoot[rootKey], ChannelThreadParticipant{
			Key:         key,
			MemberType:  memberType,
			MemberID:    memberIDText,
			Name:        firstNonEmpty(name, displayName),
			DisplayName: displayName,
			Followed:    followed,
		})
		if state, reason, ok := channelThreadWakeAnnotationState(memberType, taskStatus, taskResult, hasAgentReply); ok {
			wakeByRoot[rootKey] = append(wakeByRoot[rootKey], ChannelThreadWakeAnnotation{
				Key:         key,
				MemberType:  memberType,
				MemberID:    memberIDText,
				DisplayName: displayName,
				State:       state,
				Reason:      reason,
			})
		}
	}
	for i := range messages {
		if messages[i].ThreadRootMessageID != nil {
			continue
		}
		if participants := participantsByRoot[messages[i].ID]; len(participants) > 0 {
			messages[i].ThreadParticipants = participants
		}
		if wake := wakeByRoot[messages[i].ID]; len(wake) > 0 {
			messages[i].ThreadWakeAnnotations = wake
		}
	}
}

func channelThreadParticipantKey(memberType, memberID string) string {
	return memberType + ":" + memberID
}

func channelThreadWakeAnnotationState(memberType, taskStatus, taskResult string, hasAgentReply bool) (string, *string, bool) {
	if memberType != "agent" {
		return "", nil, false
	}
	switch taskStatus {
	case "queued":
		return "pending", nil, true
	case "dispatched", "running", "waiting_local_directory":
		return "delivered", nil, true
	case "completed":
		action, outputType, reason := channelTaskResultAction(taskResult)
		switch {
		case action == protocol.ChatOutputActionNoReply || outputType == protocol.ChatOutputKindNoReply:
			return "no_reply", reason, true
		case hasAgentReply:
			return "replied", nil, true
		case action == protocol.ChatOutputActionMessageReact || outputType == protocol.ChatOutputKindReaction:
			return "acked", nil, true
		case action == protocol.ChatOutputActionMessageSend || outputType == protocol.ChatOutputKindMessage:
			return "acked", nil, true
		default:
			return "", nil, false
		}
	default:
		if hasAgentReply {
			return "replied", nil, true
		}
		return "", nil, false
	}
}

func channelTaskResultAction(raw string) (string, string, *string) {
	var result struct {
		Action                 string `json:"action"`
		Type                   string `json:"type"`
		OutputSuppressedReason string `json:"output_suppressed_reason"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return "", "", nil
	}
	var reason *string
	if trimmed := strings.TrimSpace(result.OutputSuppressedReason); trimmed != "" {
		reason = &trimmed
	}
	return strings.TrimSpace(result.Action), strings.TrimSpace(result.Type), reason
}

func (h *Handler) UpdateChannelMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req UpdateChannelMessageRequest
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
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	msg, err := scanChannelMessage(h.DB.QueryRow(r.Context(), `
		UPDATE channel_message
		SET content = $5, parts = $6::jsonb, edited_at = now()
		WHERE id = $1
		  AND channel_id = $2
		  AND workspace_id = $3
		  AND author_type = 'user'
		  AND author_id = $4
		  AND deleted_at IS NULL
		RETURNING id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at`,
		messageID, channelID, parseUUID(workspaceID), parseUUID(userID), content, messageparts.MustJSON(parts)))
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update channel message")
		return
	}
	msg = h.attachSingleChannelMessageDetails(r.Context(), workspaceID, parseUUID(userID), msg)
	h.publishChannelToMembers(r.Context(), protocol.EventChannelMessage, workspaceID, "member", userID, channelID, msg)
	writeJSON(w, http.StatusOK, msg)
}

func (h *Handler) DeleteChannelMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete channel message")
		return
	}
	msg, err := scanChannelMessage(tx.QueryRow(r.Context(), `
		UPDATE channel_message
		SET content = '', parts = '[]'::jsonb, deleted_at = now()
		WHERE id = $1
		  AND channel_id = $2
		  AND workspace_id = $3
		  AND author_type = 'user'
		  AND author_id = $4
		  AND deleted_at IS NULL
		RETURNING id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at`,
		messageID, channelID, parseUUID(workspaceID), parseUUID(userID)))
	if err != nil {
		_ = tx.Rollback(r.Context())
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete channel message")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		DELETE FROM channel_message_reaction
		WHERE channel_message_id = $1 AND workspace_id = $2`,
		messageID, parseUUID(workspaceID)); err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusInternalServerError, "failed to delete channel message")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete channel message")
		return
	}
	msg = h.attachSingleChannelMessageDetails(r.Context(), workspaceID, parseUUID(userID), msg)
	h.publishChannelToMembers(r.Context(), protocol.EventChannelMessage, workspaceID, "member", userID, channelID, msg)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AddChannelMessageReaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req struct {
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	emoji := strings.TrimSpace(req.Emoji)
	if emoji == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	var id, msgID, actorID pgtype.UUID
	var createdAt pgtype.Timestamptz
	reaction := ChannelReactionResponse{ChannelID: uuidToString(channelID), ActorType: "member", Emoji: emoji}
	err := h.DB.QueryRow(r.Context(), `
		INSERT INTO channel_message_reaction (channel_message_id, workspace_id, actor_type, actor_id, emoji)
		SELECT id, workspace_id, 'member', $4::uuid, $5
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND author_type <> 'system' AND deleted_at IS NULL
		ON CONFLICT (channel_message_id, actor_type, actor_id, emoji) DO UPDATE SET created_at = channel_message_reaction.created_at
		RETURNING id, channel_message_id, actor_id, created_at`, messageID, channelID, parseUUID(workspaceID), parseUUID(userID), emoji).Scan(&id, &msgID, &actorID, &createdAt)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to add reaction")
		return
	}
	reaction.ID = uuidToString(id)
	reaction.MessageID = uuidToString(msgID)
	reaction.ActorID = uuidToString(actorID)
	reaction.CreatedAt = timestampToString(createdAt)
	h.publishChannelToMembers(r.Context(), protocol.EventChannelReactionAdded, workspaceID, "member", userID, channelID, map[string]any{"reaction": reaction, "channel_id": uuidToString(channelID), "message_id": uuidToString(messageID)})
	writeJSON(w, http.StatusCreated, reaction)
}

func (h *Handler) RemoveChannelMessageReaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req struct {
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	emoji := strings.TrimSpace(req.Emoji)
	if emoji == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}

	tag, err := h.DB.Exec(r.Context(), `
		DELETE FROM channel_message_reaction r
		USING channel_message m
		WHERE r.channel_message_id = m.id
		  AND r.channel_message_id = $1
		  AND m.channel_id = $2
		  AND m.author_type <> 'system'
		  AND m.deleted_at IS NULL
		  AND r.workspace_id = $3
		  AND r.actor_type = 'member'
		  AND r.actor_id = $4
		  AND r.emoji = $5`, messageID, channelID, parseUUID(workspaceID), parseUUID(userID), emoji)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove reaction")
		return
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := h.DB.QueryRow(r.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM channel_message
				WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND author_type <> 'system' AND deleted_at IS NULL
			)`, messageID, channelID, parseUUID(workspaceID)).Scan(&exists); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to remove reaction")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
	}

	h.publishChannelToMembers(r.Context(), protocol.EventChannelReactionRemoved, workspaceID, "member", userID, channelID, map[string]any{
		"channel_id": uuidToString(channelID),
		"message_id": uuidToString(messageID),
		"emoji":      emoji,
		"actor_type": "member",
		"actor_id":   userID,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListChannelMessageThread(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	rootID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	ch, found := h.getChannel(r.Context(), workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !h.requireDMChannelAgentAccess(w, r, workspaceID, userID, ch) {
		return
	}
	root, ok := h.loadChannelThreadRoot(w, r.Context(), workspaceID, channelID, rootID)
	if !ok {
		return
	}
	limit, beforeSeq, before, beforeID, useSeqCursor, hasCursor, ok := parseChannelThreadPageParams(w, r)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2 AND thread_root_message_id = $3
		  AND deleted_at IS NULL
		  AND (
		    NOT $4::boolean
		    OR ($5::boolean AND seq < $6::bigint)
		    OR (NOT $5::boolean AND (created_at, id) < ($7::timestamptz, $8::uuid))
		)
		ORDER BY seq DESC
		LIMIT $9`,
		channelID, parseUUID(workspaceID), rootID, hasCursor, useSeqCursor, beforeSeq, before, beforeID, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel thread messages")
		return
	}
	defer rows.Close()
	repliesDesc := []ChannelMessageResponse{}
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel thread messages")
			return
		}
		repliesDesc = append(repliesDesc, msg)
	}
	hasMore := len(repliesDesc) > limit
	if hasMore {
		repliesDesc = repliesDesc[:limit]
	}
	var nextCursor *ChannelThreadMessagesCursorResponse
	if hasMore && len(repliesDesc) > 0 {
		oldest := repliesDesc[len(repliesDesc)-1]
		nextCursor = &ChannelThreadMessagesCursorResponse{
			BeforeSeq: oldest.Seq,
			Before:    oldest.CreatedAt,
			BeforeID:  oldest.ID,
		}
		w.Header().Set("X-Next-Before-Seq", strconv.FormatInt(oldest.Seq, 10))
		w.Header().Set("X-Next-Before", oldest.CreatedAt)
		w.Header().Set("X-Next-Before-Id", oldest.ID)
	}
	if root.DeletedAt != nil && len(repliesDesc) == 0 {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	out := make([]ChannelMessageResponse, 0, 1+len(repliesDesc))
	out = append(out, root)
	for i := len(repliesDesc) - 1; i >= 0; i-- {
		out = append(out, repliesDesc[i])
	}
	messageIDs := make([]pgtype.UUID, len(out))
	for i, m := range out {
		messageIDs[i] = parseUUID(m.ID)
	}
	grouped := h.groupChannelMessageAttachments(r.Context(), workspaceID, messageIDs)
	for i := range out {
		out[i].Attachments = grouped[out[i].ID]
	}
	h.attachChannelMessageReactions(r.Context(), workspaceID, out)
	h.attachChannelMessageReplySummaries(r.Context(), workspaceID, out)
	h.attachChannelMessageThreadMetadata(r.Context(), workspaceID, parseUUID(userID), out[:1])
	h.attachChannelMessageThreadReadModel(r.Context(), workspaceID, out[:1])
	applyChannelMessageTombstoneReadModel(out)
	writeJSON(w, http.StatusOK, ChannelThreadMessagesPageResponse{
		Messages:   out,
		NextCursor: nextCursor,
	})
}

func (h *Handler) SendChannelMessageThreadReply(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	rootID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	var req SendChannelMessageRequest
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
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	clientMessageID, ok := normalizeChannelClientMessageID(w, req.ClientMessageID)
	if !ok {
		return
	}
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
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireDMChannelAgentAccess(w, r, workspaceID, userID, ch) {
		return
	}
	showInChannel := false
	if req.ShowInChannel != nil {
		showInChannel = *req.ShowInChannel
	} else if req.AlsoSendToChannel != nil {
		showInChannel = *req.AlsoSendToChannel
	}
	if showInChannel && ch.Kind != "group" {
		writeError(w, http.StatusBadRequest, "show_in_channel is only supported for group channels")
		return
	}
	root, ok := h.loadChannelThreadRoot(w, r.Context(), workspaceID, channelID, rootID)
	if !ok {
		return
	}
	replyToMessageID, ok := h.validateChannelThreadReplyTarget(w, r.Context(), workspaceID, channelID, rootID, req.ReplyToMessageID)
	if !ok {
		return
	}
	threadID := root.ThreadID
	if threadID == nil || strings.TrimSpace(*threadID) == "" {
		fresh := uuid.NewString()
		threadID = &fresh
	}
	authorName := h.channelAuthorName(r.Context(), userID)
	result, err := h.createUserChannelMessageWithIdempotency(r.Context(), channelMessageInsertInput{
		ChannelID:           channelID,
		WorkspaceID:         parseUUID(workspaceID),
		AuthorID:            parseUUID(userID),
		AuthorName:          authorName,
		Content:             content,
		Parts:               parts,
		ReplyToMessageID:    replyToMessageID,
		ThreadRootMessageID: rootID,
		ThreadID:            threadID,
		ClientMessageID:     clientMessageID,
		MainTimelineVisible: showInChannel,
	}, attachmentIDs)
	if err != nil {
		if errors.Is(err, errChannelClientMessageConflict) {
			writeError(w, http.StatusConflict, "client_message_id conflicts with an existing channel message")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create channel thread message")
		return
	}
	msg := result.Message
	if msg.ReplyToMessageID != nil {
		msg = h.attachChannelMessageReplySummary(r.Context(), workspaceID, msg)
	}
	if showInChannel {
		messages := []ChannelMessageResponse{msg}
		h.attachChannelMessageThreadRootSummaries(r.Context(), workspaceID, messages)
		msg = messages[0]
	}
	msg.Attachments = h.groupChannelMessageAttachments(r.Context(), workspaceID, []pgtype.UUID{parseUUID(msg.ID)})[msg.ID]
	if !result.Created {
		writeJSON(w, http.StatusOK, msg)
		return
	}
	h.followChannelThreadUser(r.Context(), channelID, rootID, parseUUID(userID), true)
	if root.Type == "user" && root.AuthorID != nil {
		h.followChannelThreadUser(r.Context(), channelID, rootID, parseUUID(*root.AuthorID), false)
	}
	h.followChannelThreadMentionedUsers(r.Context(), ch, msg)
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, channelID)
	if ch.Kind == "dm" {
		h.clearDMHiddenForChannelMembers(r.Context(), workspaceID, channelID)
	}
	h.publishChannelToMembers(r.Context(), protocol.EventChannelMessage, workspaceID, "member", userID, channelID, msg)
	if ch.Kind == "dm" {
		h.dispatchDMAgentReply(r.Context(), ch, msg, parseUUID(userID))
	} else {
		h.dispatchChannelThreadReplyMentions(r.Context(), ch, msg, parseUUID(userID))
	}
	h.sendChannelMessageToFeishu(r.Context(), ch, authorName, content)
	writeJSON(w, http.StatusCreated, msg)
}

func (h *Handler) MarkChannelThreadRead(w http.ResponseWriter, r *http.Request) {
	h.setChannelThreadReadOrFollow(w, r, true, true)
}

func (h *Handler) FollowChannelThread(w http.ResponseWriter, r *http.Request) {
	h.setChannelThreadReadOrFollow(w, r, false, true)
}

func (h *Handler) UnfollowChannelThread(w http.ResponseWriter, r *http.Request) {
	h.setChannelThreadReadOrFollow(w, r, false, false)
}

func (h *Handler) setChannelThreadReadOrFollow(w http.ResponseWriter, r *http.Request, markRead, followed bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	rootID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if _, ok := h.loadChannelThreadRoot(w, r.Context(), workspaceID, channelID, rootID); !ok {
		return
	}
	if followed {
		h.followChannelThreadUser(r.Context(), channelID, rootID, parseUUID(userID), markRead)
	} else if _, err := h.DB.Exec(r.Context(), `
		WITH updated AS (
		  UPDATE channel_thread_state
		  SET followed_at = NULL, updated_at = now()
		  WHERE root_message_id = $1 AND user_id = $2
		  RETURNING root_message_id, user_id
		)
		UPDATE thread_participant tp
		SET followed_at = NULL, wake_state = 'no_wake', updated_at = now()
		FROM updated
		WHERE tp.root_message_id = updated.root_message_id
		  AND tp.member_type = 'user'
		  AND tp.member_id = updated.user_id`, rootID, parseUUID(userID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unfollow channel thread")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) loadChannelThreadRoot(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID, rootID pgtype.UUID) (ChannelMessageResponse, bool) {
	row := h.DB.QueryRow(ctx, `
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3`,
		rootID, channelID, parseUUID(workspaceID))
	msg, err := scanChannelMessage(row)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "message not found")
			return ChannelMessageResponse{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load channel message")
		return ChannelMessageResponse{}, false
	}
	if msg.ThreadRootMessageID != nil {
		writeError(w, http.StatusBadRequest, "thread replies cannot be thread roots")
		return ChannelMessageResponse{}, false
	}
	if msg.Type == "system" {
		writeError(w, http.StatusBadRequest, "system messages cannot be thread roots")
		return ChannelMessageResponse{}, false
	}
	return msg, true
}

func (h *Handler) validateChannelThreadReplyTarget(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID, rootID pgtype.UUID, raw *string) (pgtype.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return pgtype.UUID{}, true
	}
	replyToID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*raw), "reply_to_message_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	var exists bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_message
			WHERE id = $1 AND channel_id = $2 AND workspace_id = $3
			  AND author_type <> 'system'
			  AND (id = $4 OR thread_root_message_id = $4)
		)`, replyToID, channelID, parseUUID(workspaceID), rootID).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate reply target")
		return pgtype.UUID{}, false
	}
	if !exists {
		writeError(w, http.StatusBadRequest, "reply_to_message_id must reference this thread")
		return pgtype.UUID{}, false
	}
	return replyToID, true
}

func (h *Handler) followChannelThreadUser(ctx context.Context, channelID, rootID, userID pgtype.UUID, markRead bool) {
	if _, err := h.DB.Exec(ctx, `
		WITH root AS (
		  SELECT conversation_id, workspace_id, seq
		  FROM channel_message
		  WHERE id = $2 AND channel_id = $1
		),
		thread_max AS (
		  SELECT max(m.seq) AS max_seq
		  FROM channel_message m
		  WHERE m.channel_id = $1
		    AND (m.id = $2 OR m.thread_root_message_id = $2)
		),
		upsert_state AS (
		  INSERT INTO channel_thread_state (channel_id, root_message_id, user_id, last_read_at, last_read_seq, followed_at)
		  SELECT $1, $2, $3, CASE WHEN $4 THEN now() ELSE NULL END, CASE WHEN $4 THEN COALESCE(thread_max.max_seq, root.seq) ELSE 0 END, now()
		  FROM root, thread_max
		  ON CONFLICT (root_message_id, user_id) DO UPDATE
		  SET followed_at = COALESCE(channel_thread_state.followed_at, now()),
		      last_read_at = CASE WHEN $4 THEN now() ELSE channel_thread_state.last_read_at END,
		      last_read_seq = CASE WHEN $4 THEN EXCLUDED.last_read_seq ELSE channel_thread_state.last_read_seq END,
		      updated_at = now()
		  RETURNING root_message_id, user_id, last_read_seq, followed_at, updated_at
		)
		INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, last_read_seq, followed_at, updated_at)
		SELECT root.conversation_id, upsert_state.root_message_id, 'user', upsert_state.user_id, upsert_state.last_read_seq, upsert_state.followed_at, upsert_state.updated_at
		FROM root, upsert_state
		ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		SET followed_at = EXCLUDED.followed_at,
		    wake_state = 'active',
		    last_read_seq = EXCLUDED.last_read_seq,
		    updated_at = now()`,
		channelID, rootID, userID, markRead); err != nil {
		slog.Warn("channel thread follow failed", "root", uuidToString(rootID), "user", uuidToString(userID), "error", err)
	}
}

func (h *Handler) followChannelThreadAgent(ctx context.Context, channelID, rootID, agentID pgtype.UUID) {
	if _, err := h.DB.Exec(ctx, `
		WITH root AS (
		  SELECT conversation_id
		  FROM channel_message
		  WHERE id = $2 AND channel_id = $1
		)
		INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, followed_at, updated_at)
		SELECT root.conversation_id, $2, 'agent', $3, now(), now()
		FROM root
		ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		SET followed_at = COALESCE(thread_participant.followed_at, EXCLUDED.followed_at),
		    wake_state = 'active',
		    updated_at = now()`,
		channelID, rootID, agentID); err != nil {
		slog.Warn("channel thread agent follow failed", "root", uuidToString(rootID), "agent", uuidToString(agentID), "error", err)
	}
}

func (h *Handler) followChannelThreadMentionedUsers(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse) {
	if msg.ThreadRootMessageID == nil {
		return
	}
	mentions := util.ParseMentions(msg.Content)
	if len(mentions) == 0 {
		return
	}
	members := h.channelHumanMemberIDs(ctx, ch.WorkspaceID, ch.ID)
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
	for id := range recipients {
		h.followChannelThreadUser(ctx, parseUUID(ch.ID), parseUUID(*msg.ThreadRootMessageID), parseUUID(id), false)
	}
}

func parseChannelThreadPageParams(w http.ResponseWriter, r *http.Request) (int, int64, pgtype.Timestamptz, pgtype.UUID, bool, bool, bool) {
	limit := boundedQueryInt(r, "limit", channelThreadDefaultLimit, channelThreadMaxLimit)
	beforeSeqRaw := strings.TrimSpace(r.URL.Query().Get("before_seq"))
	if beforeSeqRaw != "" {
		beforeSeq, err := strconv.ParseInt(beforeSeqRaw, 10, 64)
		if err != nil || beforeSeq < 1 {
			writeError(w, http.StatusBadRequest, "invalid before_seq parameter")
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, false
		}
		return limit, beforeSeq, pgtype.Timestamptz{}, pgtype.UUID{}, true, true, true
	}
	beforeRaw := strings.TrimSpace(r.URL.Query().Get("before"))
	beforeIDRaw := strings.TrimSpace(r.URL.Query().Get("before_id"))
	if beforeIDRaw == "" {
		beforeIDRaw = strings.TrimSpace(r.URL.Query().Get("before-id"))
	}
	if beforeRaw == "" && beforeIDRaw == "" {
		return limit, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, true
	}
	if beforeRaw == "" || beforeIDRaw == "" {
		writeError(w, http.StatusBadRequest, "before and before_id must be set together")
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, false
	}
	t, err := time.Parse(time.RFC3339Nano, beforeRaw)
	if err != nil {
		t, err = time.Parse(time.RFC3339, beforeRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid before parameter; expected RFC3339 format")
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, false
		}
	}
	id, err := util.ParseUUID(beforeIDRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid before_id parameter; expected UUID")
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, false
	}
	return limit, 0, pgtype.Timestamptz{Time: t, Valid: true}, id, false, true, true
}

func parseChannelMessagesPageParams(r *http.Request) (int, int64, pgtype.Timestamptz, pgtype.UUID, error) {
	limit := channelMessagesDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > channelMessagesMaxLimit {
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("invalid limit")
		}
		limit = parsed
	}

	if rawBeforeSeq := strings.TrimSpace(r.URL.Query().Get("before_seq")); rawBeforeSeq != "" {
		beforeSeq, err := strconv.ParseInt(rawBeforeSeq, 10, 64)
		if err != nil || beforeSeq < 1 {
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("invalid cursor")
		}
		return limit, beforeSeq, pgtype.Timestamptz{}, pgtype.UUID{}, nil
	}

	rawBeforeCreatedAt := strings.TrimSpace(r.URL.Query().Get("before_created_at"))
	rawBeforeID := strings.TrimSpace(r.URL.Query().Get("before_id"))
	if rawBeforeCreatedAt == "" && rawBeforeID == "" {
		return limit, 0, pgtype.Timestamptz{}, pgtype.UUID{}, nil
	}
	if rawBeforeCreatedAt == "" || rawBeforeID == "" {
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("invalid cursor")
	}
	beforeTime, err := time.Parse(time.RFC3339Nano, rawBeforeCreatedAt)
	if err != nil {
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("invalid cursor")
	}
	beforeID, err := util.ParseUUID(rawBeforeID)
	if err != nil {
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("invalid cursor")
	}
	return limit, 0, pgtype.Timestamptz{Time: beforeTime, Valid: true}, beforeID, nil
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
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	h.publishChannelToMembers(r.Context(), protocol.EventChannelTyping, workspaceID, "member", userID, channelID, protocol.ChannelTypingPayload{
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
	content, parts, err := messageparts.Normalize(req.Content, req.Parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	clientMessageID, ok := normalizeChannelClientMessageID(w, req.ClientMessageID)
	if !ok {
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
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireDMChannelAgentAccess(w, r, workspaceID, userID, ch) {
		return
	}
	replyToMessageID, ok := h.validateChannelReplyTarget(w, r.Context(), workspaceID, channelID, req.ReplyToMessageID)
	if !ok {
		return
	}
	authorName := h.channelAuthorName(r.Context(), userID)
	threadID := uuid.NewString()
	result, err := h.createUserChannelMessageWithIdempotency(r.Context(), channelMessageInsertInput{
		ChannelID:        channelID,
		WorkspaceID:      parseUUID(workspaceID),
		AuthorID:         parseUUID(userID),
		AuthorName:       authorName,
		Content:          content,
		Parts:            parts,
		ReplyToMessageID: replyToMessageID,
		ThreadID:         &threadID,
		ClientMessageID:  clientMessageID,
	}, attachmentIDs)
	if err != nil {
		if errors.Is(err, errChannelClientMessageConflict) {
			writeError(w, http.StatusConflict, "client_message_id conflicts with an existing channel message")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create channel message")
		return
	}
	msg := result.Message
	if msg.ReplyToMessageID != nil {
		msg = h.attachChannelMessageReplySummary(r.Context(), workspaceID, msg)
	}
	msg.Attachments = h.groupChannelMessageAttachments(r.Context(), workspaceID, []pgtype.UUID{parseUUID(msg.ID)})[msg.ID]
	if !result.Created {
		writeJSON(w, http.StatusOK, msg)
		return
	}
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, channelID)
	if ch.Kind == "dm" {
		h.clearDMHiddenForChannelMembers(r.Context(), workspaceID, channelID)
	}
	h.publishChannelToMembers(r.Context(), protocol.EventChannelMessage, workspaceID, "member", userID, channelID, msg)
	if ch.Kind == "dm" {
		// 1-on-1 DM: the agent peer (if any) replies to every user message
		// without an @-mention. Human↔human DMs have no agent member → no-op.
		h.dispatchDMAgentReply(r.Context(), ch, msg, parseUUID(userID))
	} else {
		h.dispatchChannelMessageToAgents(r.Context(), ch, msg, parseUUID(userID))
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
	if ch.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "channel is archived")
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, parseUUID(ch.ID), parseUUID(userID)) {
		return
	}
	threadID := uuid.NewString()
	msg, err := h.insertChannelMessage(r.Context(), parseUUID(ch.ID), parseUUID(workspaceID), "lark", pgtype.UUID{}, authorName, content, "lark", external, pgtype.UUID{}, pgtype.UUID{}, &threadID, 0)
	if err != nil {
		if errorsIsNoRows(err) || isUniqueViolation(err) {
			writeError(w, http.StatusNotFound, "channel not found or message already imported")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to import lark message")
		return
	}
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, parseUUID(msg.ChannelID))
	h.publishChannelToMembers(r.Context(), protocol.EventChannelMessage, workspaceID, "member", userID, parseUUID(ch.ID), msg)
	h.dispatchChannelMessageToAgents(r.Context(), ch, msg, parseUUID(userID))
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

func (h *Handler) dispatchChannelMessageToAgents(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	// Notify mentioned humans regardless of the agent trigger limit — surfacing a
	// mention to a person never feeds the automatic agent-reply loop.
	h.notifyChannelMemberMentions(ctx, ch, trigger)
	mentionedAgents := h.channelMentionedAgents(ctx, ch.WorkspaceID, ch.ID, trigger.Content)
	if len(mentionedAgents) > 0 {
		for _, agent := range mentionedAgents {
			h.dispatchChannelAgentReply(ctx, ch, agent, trigger, initiatorUserID)
		}
		return
	}
	if strings.Contains(trigger.Content, "@") {
		return
	}
	h.dispatchChannelAmbientObservation(ctx, ch, trigger, initiatorUserID)
}

func (h *Handler) dispatchChannelMentions(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	h.dispatchChannelMessageToAgents(ctx, ch, trigger, initiatorUserID)
}

func (h *Handler) dispatchChannelThreadReplyMentions(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	h.notifyChannelMemberMentions(ctx, ch, trigger)
	mentionedAgents := h.channelMentionedAgents(ctx, ch.WorkspaceID, ch.ID, trigger.Content)
	if len(mentionedAgents) > 0 {
		for _, agent := range mentionedAgents {
			h.dispatchChannelAgentReply(ctx, ch, agent, trigger, initiatorUserID)
		}
		return
	}
	if strings.Contains(trigger.Content, "@") || trigger.ThreadRootMessageID == nil {
		return
	}
	for _, agent := range h.channelThreadTargetAgents(ctx, ch.WorkspaceID, ch.ID, *trigger.ThreadRootMessageID) {
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
	if trigger.Type == "agent" && trigger.AuthorID != nil && *trigger.AuthorID == uuidToString(agent.ID) {
		return
	}
	if trigger.ThreadRootMessageID != nil {
		h.followChannelThreadAgent(ctx, parseUUID(ch.ID), parseUUID(*trigger.ThreadRootMessageID), agent.ID)
	}
	h.enqueueChannelAgentPrompt(ctx, ch, agent, trigger, initiatorUserID, h.buildChannelMentionPrompt(ctx, ch, trigger), "channel agent reply", true, true, true, false)
}

func (h *Handler) enqueueChannelAgentPrompt(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, prompt, logScope string, showTyping, forceFresh, interruptActive, lowPriority bool) {
	typingActive := false
	if showTyping {
		h.publishChannelAgentTyping(ctx, ch, agent, true)
		typingActive = true
	}
	defer func() {
		if typingActive {
			h.publishChannelAgentTyping(ctx, ch, agent, false)
		}
	}()
	session, err := h.ensureChannelAgentSession(ctx, ch, agent.ID, initiatorUserID)
	if err != nil {
		slog.Warn(logScope+": ensure chat session failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	interruptingActiveTask := false
	if interruptActive {
		interruptingActiveTask = h.interruptInFlightChannelAgentTasks(ctx, session.ID)
	}
	promptMsg, err := h.createChannelAgentPromptMessage(ctx, session.ID, prompt, trigger)
	if err != nil {
		slog.Warn(logScope+": create chat message failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	var task db.AgentTaskQueue
	if lowPriority && !interruptingActiveTask {
		task, err = h.TaskService.EnqueueAmbientChatTask(ctx, session, initiatorUserID)
	} else if interruptingActiveTask || forceFresh {
		task, err = h.TaskService.EnqueueFreshChatTask(ctx, session, initiatorUserID)
	} else {
		task, err = h.TaskService.EnqueueChatTask(ctx, session, initiatorUserID)
	}
	if err != nil {
		slog.Warn(logScope+": enqueue chat task failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	typingActive = false
	if _, err := h.DB.Exec(ctx, `UPDATE chat_message SET task_id = $1 WHERE id = $2`, task.ID, promptMsg.ID); err != nil {
		slog.Warn(logScope+": tag prompt with task failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "task", uuidToString(task.ID), "error", err)
	}
}

// dispatchChannelMemberWelcome makes every agent member of a channel post a
// short plain-text welcome when a new human joins. Each welcome is an
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
		h.publishChannelAgentTyping(ctx, ch, agent, true)
		session, err := h.ensureChannelAgentSession(ctx, ch, agent.ID, initiatorUserID)
		if err != nil {
			h.publishChannelAgentTyping(ctx, ch, agent, false)
			slog.Warn("channel welcome: ensure session failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
			continue
		}
		promptMsg, err := h.createChannelAgentPromptMessage(ctx, session.ID, prompt, synthetic)
		if err != nil {
			h.publishChannelAgentTyping(ctx, ch, agent, false)
			slog.Warn("channel welcome: create prompt failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
			continue
		}
		task, err := h.TaskService.EnqueueChatTask(ctx, session, initiatorUserID)
		if err != nil {
			h.publishChannelAgentTyping(ctx, ch, agent, false)
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
	b.WriteString("- ")
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString("- Keep it to ONE short line, in the language the channel uses (Chinese if the member's name is Chinese).\n")
	fmt.Fprintf(&b, "- During this transition, structured stickers are unavailable; welcome %s with plain text only.\n", joinedName)
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
	if msg.ThreadRootMessageID != nil {
		for id := range recipients {
			h.followChannelThreadUser(ctx, parseUUID(ch.ID), parseUUID(*msg.ThreadRootMessageID), parseUUID(id), false)
		}
	}

	// inbox_item.actor_type is constrained to member|agent|system.
	actorType := "system"
	var actorID pgtype.UUID
	switch msg.Type {
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
		if h.channelMentionRecipientMuted(ctx, ch, id) {
			continue
		}
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

func (h *Handler) channelMentionRecipientMuted(ctx context.Context, ch ChannelResponse, recipientID string) bool {
	switch ch.Kind {
	case "group":
		var muted bool
		err := h.DB.QueryRow(ctx, `
			SELECT muted_at IS NOT NULL
			FROM channel_member
			WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`,
			parseUUID(ch.ID), parseUUID(ch.WorkspaceID), parseUUID(recipientID)).Scan(&muted)
		return err == nil && muted
	case "dm":
		var peerType string
		var peerID pgtype.UUID
		err := h.DB.QueryRow(ctx, `
			SELECT member_type, member_id
			FROM channel_member
			WHERE channel_id = $1
			  AND NOT (member_type = 'user' AND member_id = $2)
			ORDER BY created_at ASC
			LIMIT 1`, parseUUID(ch.ID), parseUUID(recipientID)).Scan(&peerType, &peerID)
		if err != nil {
			return false
		}
		var muted bool
		err = h.DB.QueryRow(ctx, `
			SELECT COALESCE((
				SELECT muted_at IS NOT NULL
				FROM dm_peer_state
				WHERE workspace_id = $1 AND user_id = $2 AND peer_type = $3 AND peer_id = $4
			), false)`, parseUUID(ch.WorkspaceID), parseUUID(recipientID), peerType, peerID).Scan(&muted)
		return err == nil && muted
	default:
		return false
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

func (h *Handler) publishChannelToMembers(ctx context.Context, eventType, workspaceID, actorType, actorID string, channelID pgtype.UUID, payload any) {
	recipientIDs := recipientUserIDsFromSet(h.channelHumanMemberIDs(ctx, workspaceID, uuidToString(channelID)))
	h.publishToUsers(eventType, workspaceID, actorType, actorID, recipientIDs, payload)
}

func (h *Handler) publishChannelAgentTyping(ctx context.Context, ch ChannelResponse, agent db.Agent, isTyping bool) {
	h.publishChannelToMembers(ctx, protocol.EventChannelTyping, ch.WorkspaceID, "agent", uuidToString(agent.ID), parseUUID(ch.ID), protocol.ChannelTypingPayload{
		ChannelID:   ch.ID,
		ActorType:   "agent",
		ActorID:     uuidToString(agent.ID),
		ActorName:   agentDisplayName(agent),
		IsTyping:    isTyping,
		ExpiresInMS: channelAgentTypingExpiresInMS,
	})
}

func (h *Handler) channelMentionedAgents(ctx context.Context, workspaceID, channelID, content string) []db.Agent {
	mentions := util.ParseMentions(content)
	mentionAll := util.HasMentionAll(mentions) || contentMentionsAll(content)
	mentionedAgents := map[string]struct{}{}
	for _, mention := range mentions {
		if mention.Type == "agent" {
			mentionedAgents[mention.ID] = struct{}{}
		}
	}
	rows, err := h.DB.Query(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.visibility, a.status,
		       a.max_concurrent_tasks, a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.instructions, a.archived_at, a.display_name
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
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.AvatarUrl, &a.RuntimeMode, &a.RuntimeConfig, &a.Visibility, &a.Status, &a.MaxConcurrentTasks, &a.OwnerID, &a.CreatedAt, &a.UpdatedAt, &a.Description, &a.RuntimeID, &a.Instructions, &a.ArchivedAt, &a.DisplayName); err != nil {
			continue
		}
		_, mentionedByID := mentionedAgents[uuidToString(a.ID)]
		if mentionAll || mentionedByID || contentMentionsAgent(content, a.Name) || contentMentionsAgent(content, a.DisplayName) {
			out = append(out, a)
		}
	}
	return out
}

func (h *Handler) channelThreadTargetAgents(ctx context.Context, workspaceID, channelID, rootMessageID string) []db.Agent {
	agents := h.channelThreadAgentsFromQuery(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.visibility, a.status,
		       a.max_concurrent_tasks, a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.instructions, a.archived_at, a.display_name
		FROM channel_message m
		JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id
		JOIN channel_member cm ON cm.channel_id = m.channel_id AND cm.workspace_id = m.workspace_id AND cm.member_type = 'agent' AND cm.member_id = a.id
		WHERE m.id = $3
		  AND m.channel_id = $1
		  AND m.workspace_id = $2
		  AND a.archived_at IS NULL
		LIMIT 1`, parseUUID(channelID), parseUUID(workspaceID), parseUUID(rootMessageID))
	if len(agents) > 0 {
		return agents
	}
	agents = h.channelThreadAgentsFromQuery(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.visibility, a.status,
		       a.max_concurrent_tasks, a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.instructions, a.archived_at, a.display_name
		FROM channel_message m
		JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id
		JOIN channel_member cm ON cm.channel_id = m.channel_id AND cm.workspace_id = m.workspace_id AND cm.member_type = 'agent' AND cm.member_id = a.id
		WHERE m.channel_id = $1
		  AND m.workspace_id = $2
		  AND m.thread_root_message_id = $3
		  AND a.archived_at IS NULL
		ORDER BY m.seq DESC
		LIMIT 1`, parseUUID(channelID), parseUUID(workspaceID), parseUUID(rootMessageID))
	if len(agents) > 0 {
		return agents
	}
	agents = h.channelThreadRootMentionedAgents(ctx, workspaceID, channelID, rootMessageID)
	if len(agents) > 0 {
		return agents
	}
	return h.channelThreadAgentsFromQuery(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.visibility, a.status,
		       a.max_concurrent_tasks, a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.instructions, a.archived_at, a.display_name
		FROM chat_message cmg
		JOIN channel_agent_session cas ON cas.chat_session_id = cmg.chat_session_id
		JOIN agent a ON a.id = cas.agent_id
		JOIN channel_member cm ON cm.channel_id = cas.channel_id AND cm.workspace_id = a.workspace_id AND cm.member_type = 'agent' AND cm.member_id = a.id
		WHERE cas.channel_id = $1
		  AND a.workspace_id = $2
		  AND cmg.channel_thread_root_message_id = $3
		  AND a.archived_at IS NULL
		ORDER BY cmg.created_at DESC, cmg.id DESC
		LIMIT 1`, parseUUID(channelID), parseUUID(workspaceID), parseUUID(rootMessageID))
}

func (h *Handler) channelThreadRootMentionedAgents(ctx context.Context, workspaceID, channelID, rootMessageID string) []db.Agent {
	var content string
	if err := h.DB.QueryRow(ctx, `
		SELECT content
		FROM channel_message
		WHERE id = $3 AND channel_id = $1 AND workspace_id = $2`, parseUUID(channelID), parseUUID(workspaceID), parseUUID(rootMessageID)).Scan(&content); err != nil {
		return nil
	}
	return h.channelMentionedAgents(ctx, workspaceID, channelID, content)
}

func (h *Handler) channelThreadAgentsFromQuery(ctx context.Context, query string, args ...any) []db.Agent {
	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []db.Agent
	for rows.Next() {
		var a db.Agent
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.AvatarUrl, &a.RuntimeMode, &a.RuntimeConfig, &a.Visibility, &a.Status, &a.MaxConcurrentTasks, &a.OwnerID, &a.CreatedAt, &a.UpdatedAt, &a.Description, &a.RuntimeID, &a.Instructions, &a.ArchivedAt, &a.DisplayName); err != nil {
			continue
		}
		out = append(out, a)
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

func (h *Handler) dispatchChannelAmbientObservation(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	if trigger.Type == "agent" {
		return
	}
	for _, agent := range h.channelAgentMembers(ctx, ch.WorkspaceID, ch.ID) {
		h.dispatchSingleChannelAmbientObservation(ctx, ch, trigger, initiatorUserID, agent)
	}
}

func buildChannelAmbientObservationPrompt(ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a member of the Multica group chat #%s. A user sent a message without @-mentioning anyone.\n", ch.Name)
	b.WriteString("You can see ONLY the current message below. Do not assume any prior channel context.\n")
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString(channelAmbientNoReplyInstruction)
	b.WriteString("\n")
	b.WriteString("Decide whether your own role/profile makes a response useful. If it is not clearly relevant to you, finish without visible output; do not print no_reply or protocol text.\n")
	b.WriteString("If the message directly addresses your agent name, role, description, instructions, or an unmistakable task for you, treat it as directed to you: write a visible plain-text reply or acknowledgement, and do not return no_reply.\n")
	b.WriteString("If the message explicitly addresses everyone/all members/all agents (for example 全体, 大家, everyone, all agents) and asks for a welcome, greeting, reaction, or response, treat it as relevant to you and run `multica send` with one short visible message. Do not stay silent or print no_reply for that case.\n")
	b.WriteString("If the message asks a category of members to react (for example directors, reviewers, designers, backend engineers), respond only if your agent name/description/instructions match that category.\n")
	b.WriteString("If a lightweight acknowledgement is enough outside an all-hands welcome/greeting request, use `multica react` when a reaction is sufficient; otherwise use `multica send` with a short acknowledgement.\n")
	b.WriteString(channelStickerReplyInstruction)
	b.WriteString("\nDo not @-mention anyone from this ambient observation.\n\n")
	fmt.Fprintf(&b, "Reaction target message id: %s\n", trigger.ID)
	fmt.Fprintf(&b, "Your agent name: %s\n", agentDisplayName(agent))
	if strings.TrimSpace(agent.Description) != "" {
		fmt.Fprintf(&b, "Your agent description: %s\n", strings.TrimSpace(agent.Description))
	}
	if strings.TrimSpace(agent.Instructions) != "" {
		fmt.Fprintf(&b, "Your agent instructions: %s\n", strings.TrimSpace(agent.Instructions))
	}
	b.WriteString("\nCurrent message only:\n")
	fmt.Fprintf(&b, "%s (%s): %s", trigger.AuthorName, trigger.Type, trigger.Content)
	return b.String()
}

func (h *Handler) buildChannelMentionPrompt(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse) string {
	members := h.channelMemberSummaries(ctx, ch.WorkspaceID, ch.ID)
	messages := h.recentChannelMessages(ctx, ch.WorkspaceID, ch.ID, channelContextMessageLimit)
	if trigger.ThreadRootMessageID != nil {
		messages = h.channelThreadContextMessages(ctx, ch.WorkspaceID, ch.ID, *trigger.ThreadRootMessageID, channelContextMessageLimit)
	}
	messages = channelContextMessagesExcludingTrigger(messages, trigger.ID)

	var b strings.Builder
	fmt.Fprintf(&b, "You are participating in the Multica group chat #%s.\n", ch.Name)
	b.WriteString("Only respond as yourself. Do not impersonate other agents or users.\n")
	b.WriteString("Use the bounded channel context below, but answer the current mention directly. If key context seems missing, fetch or search more channel/thread history before guessing.\n")
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString(channelDirectedReplyInstruction)
	b.WriteString("\n")
	b.WriteString(channelStickerReplyInstruction)
	b.WriteString("\n")
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
			displayName := firstNonEmpty(member.DisplayName, member.Name)
			fmt.Fprintf(&b, "- %s (%s, @%s): [@%s](mention://%s/%s)\n", displayName, member.MemberType, member.Name, displayName, mentionType, member.MemberID)
		}
		b.WriteString("\n")
	}
	if len(messages) > 0 {
		if trigger.ThreadRootMessageID != nil {
			b.WriteString("Thread context (root message first, then bounded recent replies from this thread only):\n")
		} else {
			b.WriteString("Recent channel messages from this channel only (bounded window):\n")
		}
		for _, msg := range messages {
			fmt.Fprintf(&b, "%s\n", formatChannelMessageLine(msg))
		}
		b.WriteString("\n")
	}
	if trigger.ReplyToMessageID != nil {
		if parent, ok := h.channelMessageByID(ctx, ch.WorkspaceID, ch.ID, *trigger.ReplyToMessageID); ok {
			b.WriteString("Direct reply target for the current message:\n")
			fmt.Fprintf(&b, "%s\n\n", formatChannelMessageLine(parent))
		}
	}
	b.WriteString("Current message to respond to:\n")
	fmt.Fprintf(&b, "%s (%s): %s", trigger.AuthorName, trigger.Type, trigger.Content)
	return b.String()
}

func (h *Handler) channelMessageByID(ctx context.Context, workspaceID, channelID, messageID string) (ChannelMessageResponse, bool) {
	if strings.TrimSpace(messageID) == "" {
		return ChannelMessageResponse{}, false
	}
	row := h.DB.QueryRow(ctx, `
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3`, parseUUID(messageID), parseUUID(channelID), parseUUID(workspaceID))
	msg, err := scanChannelMessage(row)
	if err != nil {
		return ChannelMessageResponse{}, false
	}
	return msg, true
}

func (h *Handler) channelMemberSummaries(ctx context.Context, workspaceID, channelID string) []ChannelMemberResponse {
	rows, err := h.DB.Query(ctx, `
		SELECT cm.member_type, cm.member_id,
		       COALESCE(u.name, a.name, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, ''),
		       cm.created_at
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
		var typ, name, displayName string
		var id pgtype.UUID
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&typ, &id, &name, &displayName, &createdAt); err != nil {
			continue
		}
		out = append(out, ChannelMemberResponse{MemberType: typ, MemberID: uuidToString(id), Name: name, DisplayName: firstNonEmpty(displayName, name), CreatedAt: timestampToString(createdAt)})
	}
	return out
}

func (h *Handler) recentChannelMessages(ctx context.Context, workspaceID, channelID string, limit int) []ChannelMessageResponse {
	rows, err := h.DB.Query(ctx, `
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM (
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM channel_message
			WHERE channel_id = $1
			  AND workspace_id = $2
			  AND (thread_root_message_id IS NULL OR main_timeline_visible)
			  AND author_type <> 'system'
			ORDER BY seq DESC
			LIMIT $3
		) recent
		ORDER BY seq ASC`, parseUUID(channelID), parseUUID(workspaceID), limit)
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

func (h *Handler) channelThreadContextMessages(ctx context.Context, workspaceID, channelID, rootMessageID string, limit int) []ChannelMessageResponse {
	if limit < 2 {
		limit = 2
	}
	rows, err := h.DB.Query(ctx, `
		WITH replies AS (
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM channel_message
			WHERE channel_id = $1 AND workspace_id = $2 AND thread_root_message_id = $3 AND author_type <> 'system'
			ORDER BY seq DESC
			LIMIT $4
		)
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM (
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM channel_message
			WHERE id = $3 AND channel_id = $1 AND workspace_id = $2 AND author_type <> 'system'
			UNION ALL
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM replies
		) thread_context
		ORDER BY seq ASC`, parseUUID(channelID), parseUUID(workspaceID), parseUUID(rootMessageID), limit-1)
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
	return h.createChannelAgentPromptMessageWithDB(ctx, h.DB, chatSessionID, prompt, trigger)
}

func (h *Handler) createChannelAgentPromptMessageWithDB(ctx context.Context, exec db.DBTX, chatSessionID pgtype.UUID, prompt string, trigger ChannelMessageResponse) (db.ChatMessage, error) {
	threadID := trigger.ThreadID
	if threadID == nil || strings.TrimSpace(*threadID) == "" {
		fresh := uuid.NewString()
		threadID = &fresh
	}
	var threadRootMessageID any
	if trigger.ThreadRootMessageID != nil {
		threadRootMessageID = parseUUID(*trigger.ThreadRootMessageID)
	}
	row := exec.QueryRow(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, thread_id, channel_thread_root_message_id, trigger_depth)
		VALUES ($1, 'user', $2, $3, $4, $5)
		RETURNING id, chat_session_id, role, content, task_id, created_at, failure_reason, elapsed_ms, thread_id, trigger_depth, parts`,
		chatSessionID, prompt, threadID, threadRootMessageID, trigger.TriggerDepth)
	var msg db.ChatMessage
	err := row.Scan(&msg.ID, &msg.ChatSessionID, &msg.Role, &msg.Content, &msg.TaskID, &msg.CreatedAt, &msg.FailureReason, &msg.ElapsedMs, &msg.ThreadID, &msg.TriggerDepth, &msg.Parts)
	return msg, err
}

func (h *Handler) interruptInFlightChannelAgentTasks(ctx context.Context, chatSessionID pgtype.UUID) bool {
	rows, err := h.DB.Query(ctx, `
		SELECT id FROM agent_task_queue
		WHERE chat_session_id = $1
		  AND status IN ('dispatched', 'running', 'waiting_local_directory')`, chatSessionID)
	if err != nil {
		slog.Warn("channel re-mention: list in-flight tasks failed", "chat_session_id", uuidToString(chatSessionID), "error", err)
		return false
	}
	defer rows.Close()

	interrupted := false
	for rows.Next() {
		var taskID pgtype.UUID
		if err := rows.Scan(&taskID); err != nil {
			continue
		}
		if _, err := h.DB.Exec(ctx, `UPDATE agent_task_queue SET failure_reason = 'followup_interrupt' WHERE id = $1`, taskID); err != nil {
			slog.Warn("channel re-mention: mark interrupt reason failed", "task_id", uuidToString(taskID), "chat_session_id", uuidToString(chatSessionID), "error", err)
		}
		if _, err := h.TaskService.CancelTaskWithResult(ctx, taskID); err != nil {
			slog.Warn("channel re-mention: interrupt task failed", "task_id", uuidToString(taskID), "chat_session_id", uuidToString(chatSessionID), "error", err)
			continue
		}
		interrupted = true
	}
	return interrupted
}

func (h *Handler) ensureChannelAgentSession(ctx context.Context, ch ChannelResponse, agentID pgtype.UUID, creatorID pgtype.UUID) (db.ChatSession, error) {
	return h.ensureChannelAgentSessionWithDB(ctx, h.Queries, h.DB, ch, agentID, creatorID)
}

func (h *Handler) ensureChannelAgentSessionWithDB(ctx context.Context, q *db.Queries, exec db.DBTX, ch ChannelResponse, agentID pgtype.UUID, creatorID pgtype.UUID) (db.ChatSession, error) {
	var sessionID pgtype.UUID
	err := exec.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, parseUUID(ch.ID), agentID).Scan(&sessionID)
	if err == nil {
		return q.GetChatSession(ctx, sessionID)
	}
	if !errorsIsNoRows(err) {
		return db.ChatSession{}, err
	}
	session, err := q.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: parseUUID(ch.WorkspaceID),
		AgentID:     agentID,
		CreatorID:   creatorID,
		Title:       "#" + ch.Name,
	})
	if err != nil {
		return db.ChatSession{}, err
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (channel_id, agent_id) DO NOTHING`, parseUUID(ch.ID), agentID, session.ID)
	if err != nil {
		return db.ChatSession{}, err
	}
	return session, nil
}

func (h *Handler) handleChannelChatStopped(e events.Event) {
	payload, _ := e.Payload.(map[string]any)
	chatSessionID, _ := payload["chat_session_id"].(string)
	if chatSessionID == "" {
		return
	}
	if rawTaskID, _ := payload["task_id"].(string); strings.TrimSpace(rawTaskID) != "" {
		h.settleChannelAmbientWakeForTask(context.Background(), parseUUID(rawTaskID), false)
	}
	channelID, workspaceID, agentID, ok := h.channelAgentForChatSession(context.Background(), chatSessionID)
	if !ok {
		return
	}
	h.publishChannelToMembers(context.Background(), protocol.EventChannelTyping, uuidToString(workspaceID), "agent", uuidToString(agentID), channelID, protocol.ChannelTypingPayload{
		ChannelID: uuidToString(channelID),
		ActorType: "agent",
		ActorID:   uuidToString(agentID),
		ActorName: h.agentName(context.Background(), agentID),
		IsTyping:  false,
	})
}

func (h *Handler) handleChannelChatDone(e events.Event) {
	payload, ok := e.Payload.(protocol.ChatDonePayload)
	if !ok || payload.ChatSessionID == "" {
		return
	}
	ctx := context.Background()
	outputType, err := protocol.NormalizeChatOutputType(payload.Type, strings.TrimSpace(payload.Content) != "" || len(payload.Parts) > 0, payload.Reaction != nil)
	if err != nil {
		slog.Warn("channel bridge: invalid chat output type", "chat_session_id", payload.ChatSessionID, "error", err)
		return
	}
	channelID, workspaceID, agentID, ok := h.channelAgentForChatSession(ctx, payload.ChatSessionID)
	if !ok {
		return
	}
	agentName := h.agentName(ctx, agentID)
	h.publishChannelToMembers(ctx, protocol.EventChannelTyping, uuidToString(workspaceID), "agent", uuidToString(agentID), channelID, protocol.ChannelTypingPayload{
		ChannelID: uuidToString(channelID),
		ActorType: "agent",
		ActorID:   uuidToString(agentID),
		ActorName: agentName,
		IsTyping:  false,
	})
	var taskID pgtype.UUID
	if strings.TrimSpace(payload.TaskID) != "" {
		taskID = parseUUID(payload.TaskID)
	}
	if taskID.Valid {
		defer h.settleChannelAmbientWakeForTask(ctx, taskID, true)
	}
	threadID, threadRootMessageID, triggerDepth := h.channelThreadForChatTask(ctx, parseUUID(payload.ChatSessionID), taskID)
	if archived, found := h.channelIsArchived(ctx, uuidToString(workspaceID), channelID); !found || archived {
		return
	}
	reactionTargetID := h.channelReactionTargetFromPrompt(ctx, parseUUID(payload.ChatSessionID), taskID)
	if !reactionTargetID.Valid {
		reactionTargetID = threadRootMessageID
	}
	if outputType == protocol.ChatOutputKindReaction {
		h.handleChannelReactionPayload(ctx, channelID, workspaceID, agentID, reactionTargetID, payload.Reaction)
		return
	}
	if outputType == protocol.ChatOutputKindNoReply {
		slog.Debug("channel bridge: suppressed channel output", "chat_session_id", payload.ChatSessionID, "output_suppressed_reason", payload.OutputSuppressedReason)
		return
	}
	if outputType != protocol.ChatOutputKindMessage {
		return
	}
	content, parts, err := messageparts.Normalize(payload.Content, payload.Parts)
	if err != nil {
		slog.Warn("channel bridge: invalid message parts", "chat_session_id", payload.ChatSessionID, "error", err)
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}
	initiatorID := h.channelInitiatorForChatSession(ctx, parseUUID(payload.ChatSessionID))
	if h.handleTargetedChannelChatDone(ctx, chatOutputOrigin{channelID: channelID, workspaceID: workspaceID, agentID: agentID}, payload, content, parts, initiatorID) {
		return
	}
	nextDepth := triggerDepth + 1
	msg, err := h.insertChannelMessageWithParts(ctx, channelID, workspaceID, "agent", agentID, agentName, content, parts, "multica", nil, pgtype.UUID{}, threadRootMessageID, threadID, nextDepth)
	if err != nil {
		slog.Warn("channel bridge: insert agent reply failed", "chat_session_id", payload.ChatSessionID, "error", err)
		return
	}
	_, _ = h.DB.Exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, channelID)
	h.clearDMHiddenForChannelMembers(ctx, uuidToString(workspaceID), channelID)
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, uuidToString(workspaceID), "agent", uuidToString(agentID), channelID, msg)
	ch, found := h.getChannel(ctx, uuidToString(workspaceID), channelID)
	if found {
		if threadRootMessageID.Valid {
			h.dispatchChannelThreadReplyMentions(ctx, ch, msg, initiatorID)
		} else {
			h.dispatchChannelMentions(ctx, ch, msg, initiatorID)
		}
		h.sendChannelMessageToFeishu(ctx, ch, agentName, content)
	}
}

func (h *Handler) channelReactionTargetFromPrompt(ctx context.Context, chatSessionID, taskID pgtype.UUID) pgtype.UUID {
	if !taskID.Valid {
		return pgtype.UUID{}
	}
	var content string
	if err := h.DB.QueryRow(ctx, `
		SELECT content FROM chat_message
		WHERE chat_session_id = $1 AND task_id = $2 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, chatSessionID, taskID).Scan(&content); err != nil {
		return pgtype.UUID{}
	}
	for _, line := range strings.Split(content, "\n") {
		if target, ok := strings.CutPrefix(strings.TrimSpace(line), "Reaction target message id: "); ok {
			return parseUUID(strings.TrimSpace(target))
		}
	}
	return pgtype.UUID{}
}

func (h *Handler) handleChannelReactionPayload(ctx context.Context, channelID, workspaceID, agentID, triggerMessageID pgtype.UUID, reaction *protocol.ChatReactionPayload) bool {
	if reaction == nil {
		return true
	}
	messageID := triggerMessageID
	messageIDText := strings.TrimSpace(reaction.MessageID)
	if messageIDText != "" && !strings.EqualFold(messageIDText, "CURRENT_MESSAGE") {
		parsed, err := util.ParseUUID(messageIDText)
		if err != nil {
			return true
		}
		if !h.channelMessageBelongsToChannel(ctx, channelID, workspaceID, parsed) {
			return true
		}
		messageID = parsed
	}
	return h.insertChannelReactionCommand(ctx, channelID, workspaceID, agentID, messageID, strings.TrimSpace(reaction.Emoji))
}

func (h *Handler) insertChannelReactionCommand(ctx context.Context, channelID, workspaceID, agentID, messageID pgtype.UUID, emoji string) bool {
	if !messageID.Valid || strings.TrimSpace(emoji) == "" {
		return true
	}
	reaction, found, err := h.insertAgentChannelReaction(ctx, h.DB, channelID, workspaceID, agentID, messageID, emoji)
	if err != nil {
		slog.Warn("channel reaction command failed", "channel", uuidToString(channelID), "agent", uuidToString(agentID), "error", err)
		return true
	}
	if !found {
		return true
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelReactionAdded, uuidToString(workspaceID), "agent", uuidToString(agentID), channelID, map[string]any{"reaction": reaction, "channel_id": uuidToString(channelID), "message_id": uuidToString(messageID)})
	return true
}

func (h *Handler) insertAgentChannelReaction(ctx context.Context, exec dbExecutor, channelID, workspaceID, agentID, messageID pgtype.UUID, emoji string) (ChannelReactionResponse, bool, error) {
	var id, returnedMessageID, actorID pgtype.UUID
	var createdAt pgtype.Timestamptz
	err := exec.QueryRow(ctx, `
		INSERT INTO channel_message_reaction (channel_message_id, workspace_id, actor_type, actor_id, emoji)
		SELECT cm.id, cm.workspace_id, 'agent', $3, $4
		FROM channel_message cm
		WHERE cm.id = $1
		  AND cm.channel_id = $2
		  AND cm.workspace_id = $5
		  AND cm.author_type <> 'system'
		  AND cm.deleted_at IS NULL
		ON CONFLICT (channel_message_id, actor_type, actor_id, emoji) DO UPDATE SET created_at = channel_message_reaction.created_at
		RETURNING id, channel_message_id, actor_id, created_at`, messageID, channelID, agentID, emoji, workspaceID).Scan(&id, &returnedMessageID, &actorID, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChannelReactionResponse{}, false, nil
	}
	if err != nil {
		return ChannelReactionResponse{}, false, err
	}
	reaction := ChannelReactionResponse{
		ID:        uuidToString(id),
		ChannelID: uuidToString(channelID),
		MessageID: uuidToString(returnedMessageID),
		ActorType: "agent",
		ActorID:   uuidToString(actorID),
		Emoji:     emoji,
		CreatedAt: timestampToString(createdAt),
	}
	return reaction, true, nil
}

func (h *Handler) channelMessageBelongsToChannel(ctx context.Context, channelID, workspaceID, messageID pgtype.UUID) bool {
	var exists bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_message
			WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND author_type <> 'system'
			  AND deleted_at IS NULL
		)`, messageID, channelID, workspaceID).Scan(&exists); err != nil {
		return false
	}
	return exists
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

func (h *Handler) channelThreadForChatTask(ctx context.Context, chatSessionID, taskID pgtype.UUID) (*string, pgtype.UUID, int) {
	if taskID.Valid {
		if threadID, threadRootMessageID, depth, ok := h.channelThreadFromQuery(ctx, `
			SELECT thread_id, channel_thread_root_message_id, trigger_depth
			FROM chat_message
			WHERE chat_session_id = $1 AND task_id = $2 AND role = 'user'
			ORDER BY created_at DESC
			LIMIT 1`, chatSessionID, taskID); ok {
			return threadID, threadRootMessageID, depth
		}
	}
	if threadID, threadRootMessageID, depth, ok := h.channelThreadFromQuery(ctx, `
		SELECT thread_id, channel_thread_root_message_id, trigger_depth
		FROM chat_message
		WHERE chat_session_id = $1 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, chatSessionID); ok {
		return threadID, threadRootMessageID, depth
	}
	fresh := uuid.NewString()
	return &fresh, pgtype.UUID{}, 0
}

func (h *Handler) channelThreadFromQuery(ctx context.Context, query string, args ...any) (*string, pgtype.UUID, int, bool) {
	var thread pgtype.Text
	var threadRootMessageID pgtype.UUID
	var depth int
	err := h.DB.QueryRow(ctx, query, args...).Scan(&thread, &threadRootMessageID, &depth)
	if err != nil || !thread.Valid || strings.TrimSpace(thread.String) == "" {
		return nil, pgtype.UUID{}, 0, false
	}
	return &thread.String, threadRootMessageID, depth, true
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

type channelMessageInsertInput struct {
	ChannelID           pgtype.UUID
	WorkspaceID         pgtype.UUID
	AuthorID            pgtype.UUID
	AuthorName          string
	Content             string
	Parts               []protocol.MessagePart
	ReplyToMessageID    pgtype.UUID
	ThreadRootMessageID pgtype.UUID
	ThreadID            *string
	TriggerDepth        int
	ClientMessageID     *string
	MainTimelineVisible bool
}

type channelMessageCreateResult struct {
	Message ChannelMessageResponse
	Created bool
}

func normalizeChannelClientMessageID(w http.ResponseWriter, raw *string) (*string, bool) {
	if raw == nil {
		return nil, true
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, true
	}
	if len([]rune(value)) > channelClientMessageIDMaxLen {
		writeError(w, http.StatusBadRequest, "client_message_id is too long")
		return nil, false
	}
	return &value, true
}

func (h *Handler) createUserChannelMessageWithIdempotency(ctx context.Context, in channelMessageInsertInput, attachmentIDs []pgtype.UUID) (channelMessageCreateResult, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return channelMessageCreateResult{}, err
	}
	msg, err := insertChannelMessageWithPartsExec(ctx, tx, in.ChannelID, in.WorkspaceID, "user", in.AuthorID, in.AuthorName, in.Content, in.Parts, "multica", nil, in.ClientMessageID, in.ReplyToMessageID, in.ThreadRootMessageID, in.ThreadID, in.TriggerDepth, in.MainTimelineVisible)
	if err != nil {
		_ = tx.Rollback(ctx)
		if in.ClientMessageID != nil && isUniqueViolation(err) {
			return h.resolveDuplicateUserChannelMessage(ctx, in, attachmentIDs)
		}
		return channelMessageCreateResult{}, err
	}
	if len(attachmentIDs) > 0 {
		qtx := h.Queries.WithTx(tx)
		if err := qtx.LinkAttachmentsToChannelMessage(ctx, db.LinkAttachmentsToChannelMessageParams{
			ChannelMessageID: parseUUID(msg.ID),
			ChannelID:        in.ChannelID,
			Column3:          attachmentIDs,
		}); err != nil {
			_ = tx.Rollback(ctx)
			return channelMessageCreateResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return channelMessageCreateResult{}, err
	}
	return channelMessageCreateResult{Message: msg, Created: true}, nil
}

func (h *Handler) resolveDuplicateUserChannelMessage(ctx context.Context, in channelMessageInsertInput, attachmentIDs []pgtype.UUID) (channelMessageCreateResult, error) {
	existing, found, err := h.findUserChannelMessageByClientID(ctx, in.WorkspaceID, in.ChannelID, in.AuthorID, *in.ClientMessageID)
	if err != nil {
		return channelMessageCreateResult{}, err
	}
	if !found {
		return channelMessageCreateResult{}, errChannelClientMessageConflict
	}
	ok, err := h.matchesChannelMessageIdempotencyPayload(ctx, existing, in, attachmentIDs)
	if err != nil {
		return channelMessageCreateResult{}, err
	}
	if !ok {
		return channelMessageCreateResult{}, errChannelClientMessageConflict
	}
	return channelMessageCreateResult{Message: existing, Created: false}, nil
}

func (h *Handler) findUserChannelMessageByClientID(ctx context.Context, workspaceID, channelID, authorID pgtype.UUID, clientMessageID string) (ChannelMessageResponse, bool, error) {
	row := h.DB.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE workspace_id = $1 AND channel_id = $2 AND author_type = 'user' AND author_id = $3 AND client_message_id = $4`,
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

func (h *Handler) matchesChannelMessageIdempotencyPayload(ctx context.Context, existing ChannelMessageResponse, in channelMessageInsertInput, attachmentIDs []pgtype.UUID) (bool, error) {
	if existing.Content != in.Content {
		return false, nil
	}
	if string(messageparts.MustJSON(existing.Parts)) != string(messageparts.MustJSON(in.Parts)) {
		return false, nil
	}
	if !sameNullableUUID(existing.ReplyToMessageID, in.ReplyToMessageID) || !sameNullableUUID(existing.ThreadRootMessageID, in.ThreadRootMessageID) {
		return false, nil
	}
	var visible bool
	if err := h.DB.QueryRow(ctx, `
		SELECT main_timeline_visible
		FROM channel_message
		WHERE workspace_id = $1 AND id = $2`,
		in.WorkspaceID, parseUUID(existing.ID)).Scan(&visible); err != nil {
		return false, err
	}
	if visible != in.MainTimelineVisible {
		return false, nil
	}
	expectedAttachments := channelAttachmentIDSet(attachmentIDs)
	existingAttachments, err := h.channelMessageAttachmentIDSet(ctx, in.WorkspaceID, parseUUID(existing.ID))
	if err != nil {
		return false, err
	}
	return sameStringSet(existingAttachments, expectedAttachments), nil
}

func (h *Handler) channelMessageAttachmentIDSet(ctx context.Context, workspaceID, messageID pgtype.UUID) (map[string]struct{}, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT id
		FROM attachment
		WHERE channel_message_id = $1 AND workspace_id = $2`, messageID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[uuidToString(id)] = struct{}{}
	}
	return out, rows.Err()
}

func channelAttachmentIDSet(ids []pgtype.UUID) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range ids {
		if id.Valid {
			out[uuidToString(id)] = struct{}{}
		}
	}
	return out
}

func sameNullableUUID(got *string, want pgtype.UUID) bool {
	if !want.Valid {
		return got == nil || strings.TrimSpace(*got) == ""
	}
	return got != nil && *got == uuidToString(want)
}

func sameStringSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, ok := b[key]; !ok {
			return false
		}
	}
	return true
}

func (h *Handler) insertChannelMessage(ctx context.Context, channelID, workspaceID pgtype.UUID, authorType string, authorID pgtype.UUID, authorName, content, source string, externalID *string, replyToMessageID, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int) (ChannelMessageResponse, error) {
	return h.insertChannelMessageWithParts(ctx, channelID, workspaceID, authorType, authorID, authorName, content, nil, source, externalID, replyToMessageID, threadRootMessageID, threadID, triggerDepth)
}

func (h *Handler) insertChannelMessageWithParts(ctx context.Context, channelID, workspaceID pgtype.UUID, authorType string, authorID pgtype.UUID, authorName, content string, parts []protocol.MessagePart, source string, externalID *string, replyToMessageID, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int) (ChannelMessageResponse, error) {
	return h.insertChannelMessageWithPartsMainProjection(ctx, channelID, workspaceID, authorType, authorID, authorName, content, parts, source, externalID, replyToMessageID, threadRootMessageID, threadID, triggerDepth, false)
}

func (h *Handler) insertChannelMessageWithPartsMainProjection(ctx context.Context, channelID, workspaceID pgtype.UUID, authorType string, authorID pgtype.UUID, authorName, content string, parts []protocol.MessagePart, source string, externalID *string, replyToMessageID, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int, mainTimelineVisible bool) (ChannelMessageResponse, error) {
	return insertChannelMessageWithPartsExec(ctx, h.DB, channelID, workspaceID, authorType, authorID, authorName, content, parts, source, externalID, nil, replyToMessageID, threadRootMessageID, threadID, triggerDepth, mainTimelineVisible)
}

func insertChannelMessageWithPartsExec(ctx context.Context, exec dbExecutor, channelID, workspaceID pgtype.UUID, authorType string, authorID pgtype.UUID, authorName, content string, parts []protocol.MessagePart, source string, externalID, clientMessageID *string, replyToMessageID, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int, mainTimelineVisible bool) (ChannelMessageResponse, error) {
	row := exec.QueryRow(ctx, `
			INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, main_timeline_visible)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, $12, $13, $14, $15)
			RETURNING id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at`,
		channelID, workspaceID, authorType, nullableUUID(authorID), authorName, content, messageparts.MustJSON(parts), source, externalID, clientMessageID, nullableUUID(replyToMessageID), nullableUUID(threadRootMessageID), threadID, triggerDepth, mainTimelineVisible)
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

func (h *Handler) channelIsArchived(ctx context.Context, workspaceID string, channelID pgtype.UUID) (bool, bool) {
	var archivedAt pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `SELECT archived_at FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID)).Scan(&archivedAt)
	if err != nil {
		return false, false
	}
	return archivedAt.Valid, true
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

func (h *Handler) requireGroupChannel(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID) bool {
	var id pgtype.UUID
	if err := h.DB.QueryRow(ctx, `SELECT id FROM channel WHERE id = $1 AND workspace_id = $2 AND kind = 'group'`, channelID, parseUUID(workspaceID)).Scan(&id); err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	return true
}

func (h *Handler) requireChannelWritable(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID) bool {
	archived, found := h.channelIsArchived(ctx, workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	if archived {
		writeError(w, http.StatusConflict, "channel is archived")
		return false
	}
	return true
}

func (h *Handler) requireChannelManager(w http.ResponseWriter, r *http.Request, workspaceID string, channelID, userID pgtype.UUID) bool {
	var createdBy pgtype.UUID
	err := h.DB.QueryRow(r.Context(), `
		SELECT created_by
		FROM channel
		WHERE id = $1 AND workspace_id = $2 AND kind = 'group'`, channelID, parseUUID(workspaceID)).Scan(&createdBy)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	if uuidToString(createdBy) == uuidToString(userID) {
		return true
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return false
	}
	if roleAllowed(member.Role, "owner", "admin") {
		return true
	}
	writeError(w, http.StatusForbidden, "insufficient permissions")
	return false
}

func (h *Handler) getChannel(ctx context.Context, workspaceID string, channelID pgtype.UUID) (ChannelResponse, bool) {
	row := h.DB.QueryRow(ctx, `SELECT id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind, archived_at, archived_by FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID))
	ch, err := scanChannel(row)
	return ch, err == nil
}

func (h *Handler) getChannelByLarkChatID(ctx context.Context, workspaceID, larkChatID string) (ChannelResponse, bool) {
	row := h.DB.QueryRow(ctx, `SELECT id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind, archived_at, archived_by FROM channel WHERE workspace_id = $1 AND lark_chat_id = $2 LIMIT 1`, parseUUID(workspaceID), larkChatID)
	ch, err := scanChannel(row)
	return ch, err == nil
}

func (h *Handler) channelAuthorName(ctx context.Context, userID string) string {
	user, err := h.Queries.GetUser(ctx, parseUUID(userID))
	if err == nil && strings.TrimSpace(userDisplayName(user)) != "" {
		return userDisplayName(user)
	}
	if err == nil && strings.TrimSpace(user.Email) != "" {
		return user.Email
	}
	return "You"
}

func (h *Handler) agentName(ctx context.Context, agentID pgtype.UUID) string {
	agent, err := h.Queries.GetAgent(ctx, agentID)
	if err == nil && strings.TrimSpace(agentDisplayName(agent)) != "" {
		return agentDisplayName(agent)
	}
	return "Agent"
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanChannel(row rowScanner) (ChannelResponse, error) {
	var id, wsID, createdBy, archivedBy pgtype.UUID
	var name string
	var desc, lark pgtype.Text
	var createdAt, updatedAt, archivedAt pgtype.Timestamptz
	var kind string
	if err := row.Scan(&id, &wsID, &name, &desc, &lark, &createdBy, &createdAt, &updatedAt, &kind, &archivedAt, &archivedBy); err != nil {
		return ChannelResponse{}, err
	}
	return ChannelResponse{
		ID:          uuidToString(id),
		WorkspaceID: uuidToString(wsID),
		Name:        name,
		Kind:        kind,
		Description: textToPtr(desc),
		LarkChatID:  textToPtr(lark),
		CreatedBy:   uuidToString(createdBy),
		CreatedAt:   timestampToString(createdAt),
		UpdatedAt:   timestampToString(updatedAt),
		ArchivedAt:  timestampToPtr(archivedAt),
		ArchivedBy:  uuidToPtr(archivedBy),
	}, nil
}

func channelLastMessage(authorType, authorName, content string, rawParts []byte, createdAt pgtype.Timestamptz) *ChannelLastMessage {
	parts := messageparts.Decode(rawParts)
	if authorType == "agent" {
		if unwrappedContent, unwrappedParts, unwrapped, err := messageparts.UnwrapStructuredMessageSend(content, parts); err == nil && unwrapped {
			content = unwrappedContent
			parts = unwrappedParts
		}
	}
	return &ChannelLastMessage{
		Type:       authorType,
		AuthorName: authorName,
		Content:    content,
		Parts:      parts,
		CreatedAt:  timestampToString(createdAt),
	}
}

func scanChannelMessage(row rowScanner) (ChannelMessageResponse, error) {
	var id, channelID, wsID, authorID, replyToMessageID, threadRootMessageID pgtype.UUID
	var authorType, authorName, content, source string
	var parts []byte
	var external, client, thread pgtype.Text
	var triggerDepth int
	var seq int64
	var createdAt, editedAt, deletedAt pgtype.Timestamptz
	if err := row.Scan(&id, &channelID, &wsID, &authorType, &authorID, &authorName, &content, &parts, &source, &external, &client, &replyToMessageID, &threadRootMessageID, &thread, &triggerDepth, &seq, &createdAt, &editedAt, &deletedAt); err != nil {
		return ChannelMessageResponse{}, err
	}
	decodedParts := messageparts.Decode(parts)
	deletedAtPtr := timestampToPtr(deletedAt)
	if deletedAtPtr != nil {
		content = ""
		decodedParts = nil
	} else if authorType == "agent" {
		if unwrappedContent, unwrappedParts, unwrapped, err := messageparts.UnwrapStructuredMessageSend(content, decodedParts); err == nil && unwrapped {
			content = unwrappedContent
			decodedParts = unwrappedParts
		}
	}
	return ChannelMessageResponse{ID: uuidToString(id), ChannelID: uuidToString(channelID), WorkspaceID: uuidToString(wsID), Seq: seq, Type: authorType, AuthorID: uuidToPtr(authorID), AuthorName: authorName, Content: content, Parts: decodedParts, Source: source, ExternalMessageID: textToPtr(external), ClientMessageID: textToPtr(client), ReplyToMessageID: uuidToPtr(replyToMessageID), ThreadRootMessageID: uuidToPtr(threadRootMessageID), ThreadID: textToPtr(thread), TriggerDepth: triggerDepth, CreatedAt: timestampToString(createdAt), EditedAt: timestampToPtr(editedAt), DeletedAt: deletedAtPtr}, nil
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

func queryBool(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func boundedQueryInt(r *http.Request, key string, def, max int) int {
	out := def
	if raw := strings.TrimSpace(r.URL.Query().Get(key)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			out = n
		}
	}
	if out > max {
		return max
	}
	return out
}

func errorsIsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
