package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// IssueSourceMessageRequest is the minimal, caller-supplied locator for the
// message that caused an agent to create an issue. The server owns all display
// fields and normalizes a reply to its root message, so callers cannot forge a
// private discussion or choose a different thread as the return target.
type IssueSourceMessageRequest struct {
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
}

type issueSourceMessageAnchor struct {
	ChannelID pgtype.UUID
	MessageID pgtype.UUID
}

func (h *Handler) resolveIssueSourceMessageAnchor(w http.ResponseWriter, r *http.Request, workspaceID, requesterID string, req *IssueSourceMessageRequest) (issueSourceMessageAnchor, bool) {
	if req == nil {
		return issueSourceMessageAnchor{}, true
	}
	channelRaw := strings.TrimSpace(req.ChannelID)
	messageRaw := strings.TrimSpace(req.MessageID)
	if channelRaw == "" || messageRaw == "" {
		writeError(w, http.StatusBadRequest, "source.channel_id and source.message_id are required")
		return issueSourceMessageAnchor{}, false
	}
	channelID, ok := parseUUIDOrBadRequest(w, channelRaw, "source.channel_id")
	if !ok {
		return issueSourceMessageAnchor{}, false
	}
	messageID, ok := parseUUIDOrBadRequest(w, messageRaw, "source.message_id")
	if !ok {
		return issueSourceMessageAnchor{}, false
	}
	workspaceUUID := parseUUID(workspaceID)
	requesterUUID := parseUUID(requesterID)

	var foundChannel pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
		SELECT id FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, workspaceUUID).Scan(&foundChannel); err != nil {
		writeError(w, http.StatusNotFound, "source channel not found")
		return issueSourceMessageAnchor{}, false
	}
	if !h.channelUserIsMember(r.Context(), workspaceID, channelID, requesterUUID) {
		writeError(w, http.StatusForbidden, "not a source channel member")
		return issueSourceMessageAnchor{}, false
	}

	var threadRoot pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
		SELECT thread_root_message_id
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND deleted_at IS NULL`, messageID, channelID, workspaceUUID).Scan(&threadRoot); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "source message not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load source message")
		}
		return issueSourceMessageAnchor{}, false
	}
	// A request made from a reply means “track this discussion”; canonicalize
	// that to the parent/root rather than leaving an ambiguous reply anchor.
	if threadRoot.Valid {
		messageID = threadRoot
	}
	return issueSourceMessageAnchor{ChannelID: channelID, MessageID: messageID}, true
}

// issueSourceRefsForUser returns the detail-only source ref if it remains
// visible to this requester. The membership check prevents an issue shared
// broadly in a workspace from becoming a side channel into a private group.
func (h *Handler) issueSourceRefsForUser(ctx context.Context, issue db.Issue, requesterID pgtype.UUID) *IssueSourceMessageRefResponse {
	var ref IssueSourceMessageRefResponse
	var channelName string
	var channelID pgtype.UUID
	var messageID pgtype.UUID
	var threadRoot pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT c.id, c.kind, c.name, m.id, COALESCE(m.thread_root_message_id, m.id), m.content
		FROM issue_source_message src
		JOIN channel_message m ON m.id = src.message_id
		  AND m.channel_id = src.channel_id
		  AND m.workspace_id = src.workspace_id
		JOIN channel c ON c.id = src.channel_id AND c.workspace_id = src.workspace_id
		JOIN channel_member cm ON cm.channel_id = src.channel_id
		  AND cm.workspace_id = src.workspace_id
		  AND cm.member_type = 'user' AND cm.member_id = $2
		WHERE src.issue_id = $1 AND src.workspace_id = $3 AND m.deleted_at IS NULL`,
		issue.ID, requesterID, issue.WorkspaceID,
	).Scan(&channelID, &ref.ChannelKind, &channelName, &messageID, &threadRoot, &ref.Excerpt)
	if err != nil {
		return nil
	}
	ref.ChannelID = uuidToString(channelID)
	ref.MessageID = uuidToString(messageID)
	ref.ThreadRootMessageID = uuidToString(threadRoot)
	ref.Excerpt = truncateChannelHistoryContent(ref.Excerpt)
	if ref.ChannelKind == "group" && strings.TrimSpace(channelName) != "" {
		ref.ChannelName = &channelName
	}
	return &ref
}
