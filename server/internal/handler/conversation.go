package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	conversationListDefaultLimit = 50
	conversationListMaxLimit     = 100
)

// ConversationListItem is one entry in the unified Messages read model.
// Group-channel and DM payloads deliberately keep their existing shapes so
// their detail, mutation, and permission APIs can remain separate.
type ConversationListItem struct {
	Kind    string           `json:"kind"` // "channel" | "dm"
	Channel *ChannelResponse `json:"channel,omitempty"`
	DM      *DMItem          `json:"dm,omitempty"`
}

type ConversationListResponse struct {
	Items      []ConversationListItem `json:"items"`
	NextCursor *string                `json:"next_cursor,omitempty"`
}

type conversationListCursor struct {
	PinnedAt  *string `json:"pinned_at,omitempty"`
	UpdatedAt string  `json:"updated_at"`
	ID        string  `json:"id"`
}

type conversationListSortKey struct {
	pinnedAt  *time.Time
	updatedAt time.Time
	id        string
}

// ListConversations returns group channels and DMs in one read request with a
// single global order and cursor. It is intentionally read-only: create/send,
// detail, membership, preference, and permission routes remain under
// /api/channels and /api/dm.
func (h *Handler) ListConversations(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "ListConversations") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	limit, ok := parseConversationListLimit(w, r)
	if !ok {
		return
	}
	cursor, ok := parseConversationListCursor(w, r)
	if !ok {
		return
	}

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	allowedAgentIDs, ok := h.accessibleAgentIDs(r.Context(), workspaceID, actorType, actorID, member.Role)
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent access")
		return
	}

	channels, err := h.listConversationGroupChannels(r.Context(), workspaceID, userID, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list conversations")
		return
	}
	dms := h.listDMChannels(r.Context(), workspaceID, userID, allowedAgentIDs)
	dms = append(dms, h.listSupervisedAgentDMChannels(r.Context(), workspaceID, userID)...)

	items := make([]ConversationListItem, 0, len(channels)+len(dms))
	for i := range channels {
		channel := channels[i]
		items = append(items, ConversationListItem{Kind: "channel", Channel: &channel})
	}
	for i := range dms {
		dm := dms[i]
		items = append(items, ConversationListItem{Kind: "dm", DM: &dm})
	}
	sort.Slice(items, func(i, j int) bool {
		return compareConversationListKeys(conversationListItemKey(items[i]), conversationListItemKey(items[j])) < 0
	})

	if cursor != nil {
		filtered := items[:0]
		for _, item := range items {
			if compareConversationListKeys(conversationListItemKey(item), *cursor) > 0 {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	response := ConversationListResponse{Items: []ConversationListItem{}}
	if len(items) > limit {
		response.Items = items[:limit]
		response.NextCursor = encodeConversationListCursor(conversationListItemKey(response.Items[len(response.Items)-1]))
	} else {
		response.Items = items
	}
	writeJSON(w, http.StatusOK, response)
}

func parseConversationListLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return conversationListDefaultLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > conversationListMaxLimit {
		writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
		return 0, false
	}
	return limit, true
}

func parseConversationListCursor(w http.ResponseWriter, r *http.Request) (*conversationListSortKey, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if raw == "" {
		return nil, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid conversation cursor")
		return nil, false
	}
	var cursor conversationListCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		writeError(w, http.StatusBadRequest, "invalid conversation cursor")
		return nil, false
	}
	if _, err := uuid.Parse(cursor.ID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid conversation cursor")
		return nil, false
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid conversation cursor")
		return nil, false
	}
	key := &conversationListSortKey{updatedAt: updatedAt, id: cursor.ID}
	if cursor.PinnedAt != nil {
		pinnedAt, err := time.Parse(time.RFC3339Nano, *cursor.PinnedAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid conversation cursor")
			return nil, false
		}
		key.pinnedAt = &pinnedAt
	}
	return key, true
}

func encodeConversationListCursor(key conversationListSortKey) *string {
	cursor := conversationListCursor{UpdatedAt: key.updatedAt.Format(time.RFC3339Nano), ID: key.id}
	if key.pinnedAt != nil {
		pinnedAt := key.pinnedAt.Format(time.RFC3339Nano)
		cursor.PinnedAt = &pinnedAt
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return nil
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return &encoded
}

func conversationListItemKey(item ConversationListItem) conversationListSortKey {
	if item.Channel != nil {
		return newConversationListSortKey(item.Channel.PinnedAt, item.Channel.UpdatedAt, item.Channel.ID)
	}
	if item.DM != nil {
		return newConversationListSortKey(item.DM.PinnedAt, item.DM.UpdatedAt, item.DM.ID)
	}
	return conversationListSortKey{}
}

func newConversationListSortKey(pinnedAt *string, updatedAt, id string) conversationListSortKey {
	key := conversationListSortKey{id: id}
	key.updatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if pinnedAt != nil {
		parsed, err := time.Parse(time.RFC3339Nano, *pinnedAt)
		if err == nil {
			key.pinnedAt = &parsed
		}
	}
	return key
}

// compareConversationListKeys returns -1 when a sorts before b. Pinned entries
// sort globally first by newest pin, then every entry by newest activity and a
// deterministic channel-id tie breaker. The same comparator drives both first
// page order and cursor continuation.
func compareConversationListKeys(a, b conversationListSortKey) int {
	if a.pinnedAt != nil || b.pinnedAt != nil {
		if a.pinnedAt == nil {
			return 1
		}
		if b.pinnedAt == nil {
			return -1
		}
		if !a.pinnedAt.Equal(*b.pinnedAt) {
			if a.pinnedAt.After(*b.pinnedAt) {
				return -1
			}
			return 1
		}
	}
	if !a.updatedAt.Equal(b.updatedAt) {
		if a.updatedAt.After(b.updatedAt) {
			return -1
		}
		return 1
	}
	if a.id > b.id {
		return -1
	}
	if a.id < b.id {
		return 1
	}
	return 0
}

func (h *Handler) listConversationGroupChannels(ctx context.Context, workspaceID, userID string, archivedOnly bool) ([]ChannelResponse, error) {
	uid := parseUUID(userID)
	rows, err := h.DB.Query(ctx, `
		SELECT ch.id, ch.workspace_id, ch.name, ch.description, ch.lark_chat_id, ch.project_id, ch.created_by, ch.created_at, ch.updated_at, ch.kind, ch.system_key,
		       ch.archived_at, ch.archived_by, ch.avatar_url, cm.pinned_at, cm.manual_unread_at, COALESCE(vcm.muted_at, cm.muted_at),
		       cm.notify_level,
		       lm.author_type, lm.author_name, lm.content, lm.parts, lm.created_at,
		       COALESCE(vcm.main_unread_count, 0)::int,
		       GREATEST(COALESCE(vcm.main_unread_count, 0)::int, CASE WHEN cm.manual_unread_at IS NOT NULL THEN 1 ELSE 0 END),
		       COALESCE(vcm.mention_unread_count, 0),
		       NULLIF(COALESCE(vcm.last_read_seq, cr.last_read_seq, 0), 0)::bigint
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
			  AND m.workspace_id = $1
			  AND m.thread_root_message_id IS NULL
			  AND m.deleted_at IS NULL
			ORDER BY m.seq DESC LIMIT 1
		) lm ON true
		LEFT JOIN channel_read cr ON cr.channel_id = ch.id AND cr.user_id = $2
		WHERE ch.workspace_id = $1 AND ch.kind = 'group'
		  AND (($3 AND ch.archived_at IS NOT NULL) OR (NOT $3 AND ch.archived_at IS NULL))
		ORDER BY CASE WHEN $3 THEN ch.archived_at ELSE cm.pinned_at END DESC NULLS LAST,
		         ch.updated_at DESC, ch.created_at DESC`, parseUUID(workspaceID), uid, archivedOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ChannelResponse{}
	channelIDs := []pgtype.UUID{}
	for rows.Next() {
		var id, wsID, projectID, createdBy, archivedBy pgtype.UUID
		var name string
		var desc, lark, systemKey, avatarURL, lastType, lastName, lastContent, notifyLevel pgtype.Text
		var lastParts []byte
		var createdAt, updatedAt, archivedAt, pinnedAt, manualUnreadAt, mutedAt, lastAt pgtype.Timestamptz
		var realUnread, unread, mentionUnreadCount int
		var kind string
		var lastReadSeq *int64
		if err := rows.Scan(&id, &wsID, &name, &desc, &lark, &projectID, &createdBy, &createdAt, &updatedAt, &kind, &systemKey,
			&archivedAt, &archivedBy, &avatarURL, &pinnedAt, &manualUnreadAt, &mutedAt, &notifyLevel,
			&lastType, &lastName, &lastContent, &lastParts, &lastAt, &realUnread, &unread, &mentionUnreadCount, &lastReadSeq); err != nil {
			return nil, err
		}
		channel := ChannelResponse{
			ID: uuidToString(id), WorkspaceID: uuidToString(wsID), ProjectID: uuidToPtr(projectID), Name: name,
			Description: textToPtr(desc), LarkChatID: textToPtr(lark), AvatarURL: textToPtr(avatarURL), CreatedBy: uuidToString(createdBy),
			CreatedAt: timestampToString(createdAt), UpdatedAt: timestampToString(updatedAt),
			ArchivedAt: timestampToPtr(archivedAt), ArchivedBy: uuidToPtr(archivedBy), Kind: kind, SystemKey: textToPtr(systemKey),
			UnreadCount: unread, RealUnreadCount: realUnread, ManuallyUnread: manualUnreadAt.Valid,
			PinnedAt: timestampToPtr(pinnedAt), MutedAt: timestampToPtr(mutedAt), Muted: mutedAt.Valid,
			NotifyLevel: channelNotifyLevelAPI(notifyLevel), MentionUnreadCount: mentionUnreadCount, LastReadSeq: lastReadSeq,
			Members: []ChannelMemberBrief{},
		}
		if lastContent.Valid {
			channel.LastMessage = channelLastMessage(lastType.String, lastName.String, lastContent.String, lastParts, lastAt)
		}
		out = append(out, channel)
		channelIDs = append(channelIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	if len(channelIDs) == 0 {
		return out, nil
	}
	memberRows, err := h.DB.Query(ctx, `
		SELECT limited.channel_id, limited.member_type, limited.member_id,
		       COALESCE(u.name, a.name, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, ''),
		       CASE WHEN limited.member_type = 'user' THEN u.avatar_url ELSE a.avatar_url END,
		       limited.role
		FROM unnest($1::uuid[]) AS selected(channel_id)
		JOIN LATERAL (
			SELECT ranked.channel_id, ranked.member_type, ranked.member_id, ranked.role, ranked.stack_position
			FROM (
				SELECT cm.channel_id, cm.member_type, cm.member_id, cm.role,
				       row_number() OVER (
				         ORDER BY CASE cm.role WHEN 'owner' THEN 0 WHEN 'manager' THEN 1 ELSE 2 END,
				                  cm.created_at ASC, cm.member_type ASC, cm.member_id ASC
				       ) AS stack_position
				FROM channel_member cm
				WHERE cm.channel_id = selected.channel_id AND cm.workspace_id = $2
			) ranked
			WHERE ranked.stack_position <= $3
		) limited ON true
		LEFT JOIN "user" u ON limited.member_type = 'user' AND u.id = limited.member_id
		LEFT JOIN agent a ON limited.member_type = 'agent' AND a.id = limited.member_id
		ORDER BY selected.channel_id, limited.stack_position`, channelIDs, parseUUID(workspaceID), channelListMemberAvatarLimit)
	if err != nil {
		return nil, err
	}
	defer memberRows.Close()
	grouped := map[string][]ChannelMemberBrief{}
	for memberRows.Next() {
		var channelID, memberID pgtype.UUID
		var memberType, memberName, memberDisplayName, role string
		var avatarURL pgtype.Text
		if err := memberRows.Scan(&channelID, &memberType, &memberID, &memberName, &memberDisplayName, &avatarURL, &role); err != nil {
			return nil, err
		}
		if role == "" {
			role = "member"
		}
		key := uuidToString(channelID)
		grouped[key] = append(grouped[key], ChannelMemberBrief{
			MemberType: memberType, MemberID: uuidToString(memberID), Name: memberName,
			DisplayName: firstNonEmpty(memberDisplayName, memberName), AvatarURL: textToPtr(avatarURL), Role: role,
		})
	}
	if err := memberRows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if members := grouped[out[i].ID]; members != nil {
			out[i].Members = members
		}
	}
	return out, nil
}
