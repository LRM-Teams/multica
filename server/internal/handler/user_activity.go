package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	activityTabAll      = "all"
	activityTabUnread   = "unread"
	activityTabMentions = "mentions"

	activityDefaultLimit = 50
	activityMaxLimit     = 100
)

// ActivityItemResponse is a unified Activity feed row for threads and inbox items.
type ActivityItemResponse struct {
	Kind        string `json:"kind"` // "thread" | "inbox"
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`

	ChannelID    *string `json:"channel_id,omitempty"`
	ChannelName  *string `json:"channel_name,omitempty"`
	ChannelKind  *string `json:"channel_kind,omitempty"`
	UpdatedAt    string  `json:"updated_at"`
	UnreadCount  int     `json:"unread_count"`
	PreviewText  string  `json:"preview_text"`
	Title        string  `json:"title"`
	AccessDenied bool    `json:"access_denied"`

	ThreadRootMessageID *string `json:"thread_root_message_id,omitempty"`
	ReplyCount          *int    `json:"reply_count,omitempty"`
	LastReplyAt         *string `json:"last_reply_at,omitempty"`
	Followed            *bool   `json:"followed,omitempty"`
	MentionedMe         *bool   `json:"mentioned_me,omitempty"`
	Participated        *bool   `json:"participated,omitempty"`

	// LRM-809: the actor the row avatar represents. Threads: the DM peer for
	// dm channels (agent for user↔agent DMs), else the root message author.
	// Inbox rows: the inbox item actor. "system"/nil → no profile affordance.
	ActorType *string `json:"actor_type,omitempty"`
	ActorID   *string `json:"actor_id,omitempty"`

	Inbox *InboxItemResponse `json:"inbox,omitempty"`
}

type activityListResponse struct {
	Items      []ActivityItemResponse `json:"items"`
	NextCursor *string                `json:"next_cursor,omitempty"`
}

type activityThreadRow struct {
	rootMessageID    string
	channelID        string
	channelName      string
	channelKind      string
	rootContent      string
	updatedAt        time.Time
	replyCount       int
	lastReplyAt      *time.Time
	followed         bool
	wakeState        string
	participated     bool
	mentionedMe      bool
	unreadCount      int
	hasChannelAccess bool
	actorType        *string
	actorID          *string
}

// ListUserActivity returns the merged Activity feed (threads + inbox) for the
// current member. Query: tab=all|unread|mentions, cursor, limit.
func (h *Handler) ListUserActivity(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)

	tab := strings.TrimSpace(r.URL.Query().Get("tab"))
	if tab == "" {
		tab = activityTabAll
	}
	switch tab {
	case activityTabAll, activityTabUnread, activityTabMentions:
	default:
		writeError(w, http.StatusBadRequest, "tab must be all, unread, or mentions")
		return
	}

	limit := activityDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := parsePositiveInt(raw); err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		} else if n > activityMaxLimit {
			limit = activityMaxLimit
		} else {
			limit = n
		}
	}

	ctx := r.Context()
	threads, err := h.loadActivityThreads(ctx, wsUUID, userUUID)
	if err != nil {
		slog.Warn("activity: load threads failed", "workspace", workspaceID, "user", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list activity")
		return
	}

	inboxRows, err := h.Queries.ListInboxItems(ctx, db.ListInboxItemsParams{
		WorkspaceID:   wsUUID,
		RecipientType: "member",
		RecipientID:   userUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activity")
		return
	}

	items := h.mergeActivityFeed(workspaceID, tab, threads, inboxRows)
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if cursor != "" {
		items = activityItemsAfterCursor(items, cursor)
	}
	var nextCursor *string
	if len(items) > limit {
		cursorValue := activityItemCursor(items[limit-1])
		nextCursor = &cursorValue
		items = items[:limit]
	}
	if items == nil {
		items = []ActivityItemResponse{}
	}

	writeJSON(w, http.StatusOK, activityListResponse{Items: items, NextCursor: nextCursor})
}

// MarkAllUserActivityRead marks all thread and inbox activity as read for the user.
func (h *Handler) MarkAllUserActivityRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	ctx := r.Context()

	threads, err := h.loadActivityThreads(ctx, wsUUID, userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark activity read")
		return
	}
	threadMarked := 0
	for _, row := range threads {
		if !activityThreadRelevantForAll(row) || row.unreadCount <= 0 || !row.hasChannelAccess {
			continue
		}
		h.markChannelThreadUserRead(ctx, parseUUID(row.channelID), parseUUID(row.rootMessageID), userUUID)
		threadMarked++
	}

	inboxCount, err := h.Queries.MarkAllInboxRead(ctx, db.MarkAllInboxReadParams{
		WorkspaceID: wsUUID,
		RecipientID: userUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark activity read")
		return
	}

	slog.Info("activity: mark all read", append(logger.RequestAttrs(r), "user_id", userID, "threads", threadMarked, "inbox", inboxCount)...)
	h.publish(protocol.EventInboxBatchRead, workspaceID, "member", userID, map[string]any{
		"recipient_id": userID,
		"count":        inboxCount,
		"threads":      threadMarked,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"thread_count": threadMarked,
		"inbox_count":  inboxCount,
	})
}

func (h *Handler) loadActivityThreads(ctx context.Context, workspaceID, userID pgtype.UUID) ([]activityThreadRow, error) {
	rows, err := h.DB.Query(ctx, `
		WITH mention_roots AS (
		  SELECT DISTINCT COALESCE(m.thread_root_message_id, m.id) AS root_message_id
		  FROM channel_message m
		  WHERE m.workspace_id = $1
		    AND m.deleted_at IS NULL
		    AND EXISTS (
		      SELECT 1
		      FROM jsonb_array_elements(COALESCE(m.parts, '[]'::jsonb)) AS part(value)
		      WHERE part.value->>'type' = 'reference'
		        AND part.value->>'ref_type' = 'mention'
		        AND part.value->>'ref_subtype' = 'member'
		        AND (part.value->>'ref_id')::uuid = $2
		    )
		),
		participated_roots AS (
		  SELECT DISTINCT m.thread_root_message_id AS root_message_id
		  FROM channel_message m
		  WHERE m.workspace_id = $1
		    AND m.thread_root_message_id IS NOT NULL
		    AND m.author_type = 'user'
		    AND m.author_id = $2
		    AND m.deleted_at IS NULL
		),
		candidate_roots AS (
		  SELECT root_message_id FROM mention_roots
		  UNION
		  SELECT root_message_id FROM participated_roots
		  UNION
		  SELECT tp.root_message_id
		  FROM thread_participant tp
		  WHERE tp.member_type = 'user' AND tp.member_id = $2
		)
		SELECT
		  root.id::text,
		  root.channel_id::text,
		  ch.name,
		  ch.kind,
		  root.content,
		  GREATEST(root.created_at, COALESCE(max(replies.created_at), root.created_at)) AS updated_at,
		  count(replies.id)::int AS reply_count,
		  max(replies.created_at) AS last_reply_at,
		  COALESCE(tp.followed_at IS NOT NULL, false) AS followed,
		  COALESCE(tp.wake_state, 'no_wake') AS wake_state,
		  EXISTS (SELECT 1 FROM participated_roots p WHERE p.root_message_id = root.id) AS participated,
		  EXISTS (SELECT 1 FROM mention_roots mm WHERE mm.root_message_id = root.id) AS mentioned_me,
		  CASE
		    WHEN COALESCE(tp.wake_state, 'no_wake') = 'unfollowed' THEN 0
		    WHEN COALESCE(tp.followed_at IS NOT NULL, false)
		      OR EXISTS (SELECT 1 FROM participated_roots p WHERE p.root_message_id = root.id)
		      OR EXISTS (SELECT 1 FROM mention_roots mm WHERE mm.root_message_id = root.id)
		    THEN count(replies.id) FILTER (
		      WHERE replies.seq > COALESCE(tp.last_read_seq, 0)
		        AND replies.author_type <> 'system'
		        AND NOT (replies.author_type = 'user' AND replies.author_id = $2)
		    )::int
		    ELSE 0
		  END AS unread_count,
		  EXISTS (
		    SELECT 1
		    FROM channel_member cm
		    WHERE cm.channel_id = root.channel_id
		      AND cm.member_type = 'user'
		      AND cm.member_id = $2
		  ) AS has_channel_access,
		  -- LRM-809: row avatar actor. dm → the peer member (agent preferred for
		  -- user↔agent DMs; the other user for human DMs); otherwise the root
		  -- message author. NULL when the dm has no peer (degenerate channel).
		  CASE WHEN ch.kind = 'dm' THEN (
		    SELECT cm2.member_type
		    FROM channel_member cm2
		    WHERE cm2.channel_id = root.channel_id
		      AND NOT (cm2.member_type = 'user' AND cm2.member_id = $2)
		    ORDER BY cm2.member_type, cm2.member_id
		    LIMIT 1
		  ) ELSE root.author_type END AS actor_type,
		  CASE WHEN ch.kind = 'dm' THEN (
		    SELECT cm2.member_id::text
		    FROM channel_member cm2
		    WHERE cm2.channel_id = root.channel_id
		      AND NOT (cm2.member_type = 'user' AND cm2.member_id = $2)
		    ORDER BY cm2.member_type, cm2.member_id
		    LIMIT 1
		  ) ELSE root.author_id::text END AS actor_id
		FROM candidate_roots cr
		JOIN channel_message root
		  ON root.id = cr.root_message_id
		 AND root.workspace_id = $1
		 AND root.deleted_at IS NULL
		 AND root.thread_root_message_id IS NULL
		JOIN channel ch ON ch.id = root.channel_id
		LEFT JOIN thread_participant tp
		  ON tp.root_message_id = root.id
		 AND tp.member_type = 'user'
		 AND tp.member_id = $2
		LEFT JOIN channel_message replies
		  ON replies.thread_root_message_id = root.id
		 AND replies.deleted_at IS NULL
		GROUP BY root.id, root.channel_id, ch.name, ch.kind, root.content, root.created_at,
		         tp.followed_at, tp.wake_state, tp.last_read_seq`,
		workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []activityThreadRow{}
	for rows.Next() {
		var row activityThreadRow
		var lastReplyAt pgtype.Timestamptz
		if err := rows.Scan(
			&row.rootMessageID,
			&row.channelID,
			&row.channelName,
			&row.channelKind,
			&row.rootContent,
			&row.updatedAt,
			&row.replyCount,
			&lastReplyAt,
			&row.followed,
			&row.wakeState,
			&row.participated,
			&row.mentionedMe,
			&row.unreadCount,
			&row.hasChannelAccess,
			&row.actorType,
			&row.actorID,
		); err != nil {
			return nil, err
		}
		if lastReplyAt.Valid {
			t := lastReplyAt.Time
			row.lastReplyAt = &t
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (h *Handler) mergeActivityFeed(workspaceID, tab string, threads []activityThreadRow, inbox []db.ListInboxItemsRow) []ActivityItemResponse {
	items := make([]ActivityItemResponse, 0, len(threads)+len(inbox))

	for _, row := range threads {
		if !activityThreadMatchesTab(row, tab) {
			continue
		}
		items = append(items, activityThreadToResponse(workspaceID, row))
	}

	for _, item := range inbox {
		if isChannelMentionInboxRow(item) {
			continue
		}
		resp := inboxRowToResponse(item)
		if !activityInboxMatchesTab(resp, tab) {
			continue
		}
		unread := 0
		if !resp.Read {
			unread = 1
		}
		items = append(items, ActivityItemResponse{
			Kind:        "inbox",
			ID:          resp.ID,
			WorkspaceID: resp.WorkspaceID,
			UpdatedAt:   resp.CreatedAt,
			UnreadCount: unread,
			PreviewText: activityPreviewText(textOrEmpty(resp.Body)),
			Title:       resp.Title,
			ActorType:   resp.ActorType,
			ActorID:     resp.ActorID,
			Inbox:       &resp,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt != items[j].UpdatedAt {
			return items[i].UpdatedAt > items[j].UpdatedAt
		}
		return items[i].ID > items[j].ID
	})
	return items
}

func activityThreadToResponse(workspaceID string, row activityThreadRow) ActivityItemResponse {
	followed := row.followed
	mentioned := row.mentionedMe
	participated := row.participated
	replyCount := row.replyCount
	lastReplyAt := timestampPtrFromTime(row.lastReplyAt)
	channelID := row.channelID
	channelName := row.channelName
	channelKind := row.channelKind
	title := activityThreadTitle(row.channelName, row.channelKind, row.rootContent)

	return ActivityItemResponse{
		Kind:                "thread",
		ID:                  row.rootMessageID,
		WorkspaceID:         workspaceID,
		ChannelID:           &channelID,
		ChannelName:         &channelName,
		ChannelKind:         &channelKind,
		UpdatedAt:           row.updatedAt.UTC().Format(time.RFC3339Nano),
		UnreadCount:         row.unreadCount,
		PreviewText:         activityPreviewText(row.rootContent),
		Title:               title,
		AccessDenied:        !row.hasChannelAccess,
		ThreadRootMessageID: stringPtr(row.rootMessageID),
		ReplyCount:          &replyCount,
		LastReplyAt:         lastReplyAt,
		Followed:            &followed,
		MentionedMe:         &mentioned,
		Participated:        &participated,
		ActorType:           row.actorType,
		ActorID:             row.actorID,
	}
}

func activityThreadRelevantForAll(row activityThreadRow) bool {
	return row.wakeState != "unfollowed" && (row.followed || row.participated || row.mentionedMe)
}

func activityThreadMatchesTab(row activityThreadRow, tab string) bool {
	switch tab {
	case activityTabAll:
		return activityThreadRelevantForAll(row)
	case activityTabUnread:
		return activityThreadRelevantForAll(row) && row.unreadCount > 0
	case activityTabMentions:
		return row.mentionedMe
	default:
		return false
	}
}

func activityInboxMatchesTab(item InboxItemResponse, tab string) bool {
	switch tab {
	case activityTabAll:
		return true
	case activityTabUnread:
		return !item.Read
	case activityTabMentions:
		return item.Type == "mentioned"
	default:
		return false
	}
}

func isChannelMentionInboxRow(item db.ListInboxItemsRow) bool {
	return item.Type == "mentioned" && !item.IssueID.Valid
}

func activityThreadTitle(channelName, channelKind, rootContent string) string {
	prefix := "#" + channelName
	if channelKind == "dm" {
		prefix = channelName
	}
	snippet := strings.TrimSpace(rootContent)
	if runes := []rune(snippet); len(runes) > 80 {
		snippet = string(runes[:80])
	}
	if snippet == "" {
		return prefix
	}
	return prefix + ": " + snippet
}

func activityPreviewText(content string) string {
	body := strings.TrimSpace(content)
	if runes := []rune(body); len(runes) > 280 {
		return string(runes[:280])
	}
	return body
}

func textOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timestampPtrFromTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	return stringPtr(value.UTC().Format(time.RFC3339Nano))
}

func activityItemCursor(item ActivityItemResponse) string {
	return item.UpdatedAt + "|" + item.ID
}

func activityItemsAfterCursor(items []ActivityItemResponse, cursor string) []ActivityItemResponse {
	parts := strings.SplitN(cursor, "|", 2)
	if len(parts) != 2 {
		return items
	}
	cursorAt, cursorID := parts[0], parts[1]
	out := []ActivityItemResponse{}
	for _, item := range items {
		if item.UpdatedAt < cursorAt {
			out = append(out, item)
			continue
		}
		if item.UpdatedAt == cursorAt && item.ID < cursorID {
			out = append(out, item)
		}
	}
	return out
}

func parsePositiveInt(raw string) (int, error) {
	return strconv.Atoi(raw)
}
