package handler

import (
	"context"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/conversationhandle"
)

type conversationHandleLookupResponse struct {
	Available bool    `json:"available"`
	Href      *string `json:"href,omitempty"`
}

// LookupConversationHandle is the human read path for the shared conversation
// handle grammar (#channel, #channel:<threadShortId>, dm:@peer).
func (h *Handler) LookupConversationHandle(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "LookupConversationHandle") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	href, ok := h.lookupConversationHandleHref(r.Context(), ctxWorkspaceID(r.Context()), userID, r.URL.Query().Get("handle"))
	if !ok {
		writeJSON(w, http.StatusOK, conversationHandleLookupResponse{Available: false})
		return
	}
	writeJSON(w, http.StatusOK, conversationHandleLookupResponse{Available: true, Href: &href})
}

func (h *Handler) lookupConversationHandleHref(ctx context.Context, workspaceID, userID, raw string) (string, bool) {
	handle, ok := conversationhandle.Parse(raw)
	if !ok || handle.Kind != conversationhandle.KindChannel {
		return "", false
	}
	var channelID pgtype.UUID
	var workspaceSlug string
	err := h.DB.QueryRow(ctx, `
		SELECT ch.id, ws.slug
		FROM channel ch
		JOIN workspace ws ON ws.id = ch.workspace_id
		JOIN channel_member viewer
		  ON viewer.channel_id = ch.id
		 AND viewer.workspace_id = ch.workspace_id
		 AND viewer.member_type = 'user'
		 AND viewer.member_id = $2
		WHERE ch.workspace_id = $1
		  AND ch.name = $3
		  AND ch.kind = 'group'
		  AND ch.archived_at IS NULL`, workspaceID, userID, handle.Name).Scan(&channelID, &workspaceSlug)
	if err != nil {
		return "", false
	}
	base := "/" + url.PathEscape(workspaceSlug) + "/channels/" + url.PathEscape(uuidToString(channelID))
	if handle.MessagePrefix == "" {
		return base, true
	}
	messageID, threadRoot, ok := h.lookupUniqueMessageByPrefix(ctx, parseUUID(workspaceID), channelID, handle.MessagePrefix)
	if !ok {
		return "", false
	}
	if threadRoot.Valid {
		return base + "?thread=" + url.QueryEscape(uuidToString(threadRoot)) + "&message=" + url.QueryEscape(uuidToString(messageID)), true
	}
	return base + "?message=" + url.QueryEscape(uuidToString(messageID)), true
}

func (h *Handler) lookupUniqueMessageByPrefix(ctx context.Context, workspaceID, channelID pgtype.UUID, prefix string) (pgtype.UUID, pgtype.UUID, bool) {
	rows, err := h.DB.Query(ctx, `
		SELECT id, thread_root_message_id
		FROM channel_message
		WHERE workspace_id = $1
		  AND channel_id = $2
		  AND deleted_at IS NULL
		  AND author_type IN ('user', 'agent')
		  AND replace(id::text, '-', '') LIKE $3 || '%'
		ORDER BY id ASC
		LIMIT 2`, workspaceID, channelID, prefix)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	defer rows.Close()
	var matches [][2]pgtype.UUID
	for rows.Next() {
		var id, root pgtype.UUID
		if err := rows.Scan(&id, &root); err != nil {
			return pgtype.UUID{}, pgtype.UUID{}, false
		}
		matches = append(matches, [2]pgtype.UUID{id, root})
	}
	if err := rows.Err(); err != nil || len(matches) != 1 {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	return matches[0][0], matches[0][1], true
}
