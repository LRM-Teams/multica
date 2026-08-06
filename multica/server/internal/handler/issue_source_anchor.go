package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/messageparts"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
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

// IssueSourceChannelRequest updates the one canonical discussion-context
// anchor for an issue. A UUID associates a group; null or an empty string
// clears it. Message-backed chat origins keep using IssueSourceMessageRequest.
type IssueSourceChannelRequest struct {
	ChannelID json.RawMessage `json:"channel_id"`
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
	actorType, actorID := h.resolveActor(r, requesterID, workspaceID)
	if actorType == "agent" {
		if !h.requireChannelAgentMember(w, r.Context(), workspaceID, channelID, parseUUID(actorID)) {
			return issueSourceMessageAnchor{}, false
		}
	} else if !h.channelUserIsMember(r.Context(), workspaceID, channelID, requesterUUID) {
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

// resolveIssueSourceChannelAnchor validates an explicit group association that
// has no source message. The caller must be able to see the group, otherwise
// this endpoint would turn an issue into a side channel for private groups.
func (h *Handler) resolveIssueSourceChannelAnchor(w http.ResponseWriter, r *http.Request, workspaceID, requesterID string, raw json.RawMessage) (pgtype.UUID, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return pgtype.UUID{}, true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid channel_id")
		return pgtype.UUID{}, false
	}
	if value = strings.TrimSpace(value); value == "" {
		return pgtype.UUID{}, true
	}
	channelID, ok := parseUUIDOrBadRequest(w, value, "channel_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	actorType, actorID := h.resolveActor(r, requesterID, workspaceID)
	if actorType == "agent" {
		if !h.requireChannelAgentMember(w, r.Context(), workspaceID, channelID, parseUUID(actorID)) {
			return pgtype.UUID{}, false
		}
	} else if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(requesterID)) {
		return pgtype.UUID{}, false
	}
	if !h.requireGroupChannel(w, r.Context(), workspaceID, channelID) {
		return pgtype.UUID{}, false
	}
	return channelID, true
}

// SetIssueSourceChannel sets, switches, or clears the issue's canonical group
// association. It deliberately reuses issue_source_message instead of adding
// issue.channel_id: an optional message id distinguishes a message-originated
// discussion from a group-only association while the issue remains 1:1.
func (h *Handler) SetIssueSourceChannel(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	requesterID := requestUserID(r)
	var req IssueSourceChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.ChannelID) == 0 {
		writeError(w, http.StatusBadRequest, "channel_id is required; use null to clear")
		return
	}
	channelID, ok := h.resolveIssueSourceChannelAnchor(w, r, uuidToString(issue.WorkspaceID), requesterID, req.ChannelID)
	if !ok {
		return
	}
	if !channelID.Valid {
		if _, err := h.DB.Exec(r.Context(), `DELETE FROM issue_source_message WHERE issue_id = $1 AND workspace_id = $2`, issue.ID, issue.WorkspaceID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear issue channel")
			return
		}
	} else if _, err := h.DB.Exec(r.Context(), `
		INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
		VALUES ($1, $2, $3, NULL)
		ON CONFLICT (issue_id) DO UPDATE
		SET workspace_id = EXCLUDED.workspace_id, channel_id = EXCLUDED.channel_id, message_id = NULL`,
		issue.ID, issue.WorkspaceID, channelID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set issue channel")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	resp := issueToResponse(issue, prefix)
	actorType, actorID := h.resolveActor(r, requesterID, uuidToString(issue.WorkspaceID))
	if channelID.Valid {
		if refs := h.issueSourceRefsForActor(r.Context(), issue, actorType, parseUUID(actorID)); refs != nil {
			resp.SourceRefs = refs
		}
	}
	h.publish(protocol.EventIssueUpdated, uuidToString(issue.WorkspaceID), actorType, actorID, map[string]any{
		"issue": resp,
	})
	writeJSON(w, http.StatusOK, resp)
}

// issueSourceRefsForActor returns the detail-only source ref if it remains
// visible to the resolved caller. The membership check prevents an issue shared
// broadly in a workspace from becoming a side channel into a private group.
func (h *Handler) issueSourceRefsForActor(ctx context.Context, issue db.Issue, actorType string, actorID pgtype.UUID) *IssueSourceRefsResponse {
	if actorType != "member" && actorType != "agent" {
		return nil
	}
	membershipType := actorType
	if membershipType == "member" {
		membershipType = "user"
	}
	var channelRef IssueSourceChannelRefResponse
	var messageRef IssueSourceMessageRefResponse
	var channelName string
	var channelID pgtype.UUID
	var messageID pgtype.UUID
	var threadRoot pgtype.UUID
	var content pgtype.Text
	var rawParts []byte
	err := h.DB.QueryRow(ctx, `
		SELECT c.id, c.kind, c.name, m.id, COALESCE(m.thread_root_message_id, m.id), m.content, m.parts
		FROM issue_source_message src
		LEFT JOIN channel_message m ON m.id = src.message_id
		  AND m.channel_id = src.channel_id
		  AND m.workspace_id = src.workspace_id
		  AND m.deleted_at IS NULL
		JOIN channel c ON c.id = src.channel_id AND c.workspace_id = src.workspace_id
		JOIN channel_member cm ON cm.channel_id = src.channel_id
		  AND cm.workspace_id = src.workspace_id
		  AND cm.member_type = $2 AND cm.member_id = $3
		WHERE src.issue_id = $1 AND src.workspace_id = $4`,
		issue.ID, membershipType, actorID, issue.WorkspaceID,
	).Scan(&channelID, &channelRef.ChannelKind, &channelName, &messageID, &threadRoot, &content, &rawParts)
	if err != nil {
		return nil
	}
	channelRef.ChannelID = uuidToString(channelID)
	if channelRef.ChannelKind == "group" && strings.TrimSpace(channelName) != "" {
		channelRef.ChannelName = &channelName
	}
	refs := &IssueSourceRefsResponse{Channel: &channelRef}
	if !messageID.Valid {
		return refs
	}
	messageRef.ChannelID = channelRef.ChannelID
	messageRef.ChannelKind = channelRef.ChannelKind
	messageRef.ChannelName = channelRef.ChannelName
	messageRef.MessageID = uuidToString(messageID)
	messageRef.ThreadRootMessageID = uuidToString(threadRoot)
	messageRef.Excerpt = truncateChannelHistoryContent(content.String)
	messageRef.ExcerptParts = sourceExcerptReferenceParts(content.String, messageRef.Excerpt, messageparts.Decode(rawParts))
	refs.Message = &messageRef
	return refs
}

// sourceExcerptReferenceParts returns only references that remain wholly in
// the visible canonical-content prefix used by an excerpt. The spans therefore
// remain valid for the excerpt without asking a client to search text or to
// reinterpret a reference that was cut in half. Content with CR normalization
// is deliberately omitted: it is rare, and a wrong anchor is worse than an
// unstyled readable fallback.
func sourceExcerptReferenceParts(content, excerpt string, parts []protocol.MessagePart) []protocol.MessagePart {
	if strings.Contains(content, "\r") || len(parts) == 0 {
		return nil
	}

	prefix, truncated := sourceExcerptPrefix(content)
	expectedExcerpt := prefix
	if truncated {
		expectedExcerpt += "\n...[truncated]"
	}
	if excerpt != expectedExcerpt {
		return nil
	}

	prefixEnd := contentUTF16Offset(content, len(prefix))
	out := make([]protocol.MessagePart, 0, len(parts))
	for _, part := range parts {
		if part.Type != protocol.MessagePartTypeReference || part.ContentStartUTF16 == nil || part.ContentEndUTF16 == nil {
			continue
		}
		if *part.ContentStartUTF16 < 0 || *part.ContentEndUTF16 < *part.ContentStartUTF16 || *part.ContentEndUTF16 > prefixEnd {
			continue
		}
		out = append(out, part)
	}
	return out
}

// sourceExcerptPrefix mirrors truncateChannelHistoryContent while retaining
// the canonical-content prefix that its UTF-16 anchors refer to.
func sourceExcerptPrefix(content string) (string, bool) {
	truncated := false
	lines := strings.Split(content, "\n")
	if len(lines) > channelHistoryMessageMaxLines {
		lines = lines[:channelHistoryMessageMaxLines]
		truncated = true
	}
	prefix := strings.Join(lines, "\n")
	runes := []rune(prefix)
	if len(runes) > channelHistoryMessageMaxChars {
		prefix = string(runes[:channelHistoryMessageMaxChars])
		truncated = true
	}
	return strings.TrimRight(prefix, " \t\n"), truncated
}
