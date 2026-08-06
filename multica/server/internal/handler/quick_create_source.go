package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const quickCreateSourceMessageLimit = 8

type quickCreateSourceMessage struct {
	ID                  pgtype.UUID
	AuthorType          string
	AuthorID            pgtype.UUID
	AuthorName          string
	Content             string
	ThreadRootMessageID pgtype.UUID
	ThreadID            pgtype.Text
	CreatedAt           pgtype.Timestamptz
}

func (h *Handler) resolveQuickCreateSourceContext(w http.ResponseWriter, r *http.Request, workspaceID string, requesterID pgtype.UUID, req *QuickCreateIssueSourceRequest) (*protocol.QuickCreateSourceContext, bool) {
	if req == nil {
		return nil, true
	}
	channelIDRaw := strings.TrimSpace(req.ChannelID)
	messageIDRaw := strings.TrimSpace(req.MessageID)
	rootIDRaw := strings.TrimSpace(req.ThreadRootMessageID)
	if channelIDRaw == "" && messageIDRaw == "" && rootIDRaw == "" {
		return nil, true
	}
	if channelIDRaw == "" {
		writeError(w, http.StatusBadRequest, "source.channel_id is required")
		return nil, false
	}
	if messageIDRaw == "" && rootIDRaw == "" {
		writeError(w, http.StatusBadRequest, "source.message_id or source.thread_root_message_id is required")
		return nil, false
	}
	if messageIDRaw == "" {
		messageIDRaw = rootIDRaw
	}

	channelID, ok := parseUUIDOrBadRequest(w, channelIDRaw, "source.channel_id")
	if !ok {
		return nil, false
	}
	messageID, ok := parseUUIDOrBadRequest(w, messageIDRaw, "source.message_id")
	if !ok {
		return nil, false
	}

	var channelName, channelKind string
	var archivedAt pgtype.Timestamptz
	if err := h.DB.QueryRow(r.Context(), `
		SELECT name, kind, archived_at
		FROM channel
		WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID)).
		Scan(&channelName, &channelKind, &archivedAt); err != nil {
		writeError(w, http.StatusNotFound, "source channel not found")
		return nil, false
	}
	if archivedAt.Valid {
		writeError(w, http.StatusConflict, "source channel is archived")
		return nil, false
	}
	if !h.channelUserIsMember(r.Context(), workspaceID, channelID, requesterID) {
		writeError(w, http.StatusForbidden, "not a source channel member")
		return nil, false
	}

	sourceMsg, err := h.loadQuickCreateSourceMessage(r.Context(), parseUUID(workspaceID), channelID, messageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "source message not found")
		return nil, false
	}

	rootID := messageID
	if rootIDRaw != "" {
		rootID, ok = parseUUIDOrBadRequest(w, rootIDRaw, "source.thread_root_message_id")
		if !ok {
			return nil, false
		}
	} else if sourceMsg.ThreadRootMessageID.Valid {
		rootID = sourceMsg.ThreadRootMessageID
	}

	rootMsg := sourceMsg
	if uuidToString(rootID) != uuidToString(sourceMsg.ID) {
		if !sourceMsg.ThreadRootMessageID.Valid || uuidToString(sourceMsg.ThreadRootMessageID) != uuidToString(rootID) {
			writeError(w, http.StatusBadRequest, "source message is not in source thread")
			return nil, false
		}
		rootMsg, err = h.loadQuickCreateSourceMessage(r.Context(), parseUUID(workspaceID), channelID, rootID)
		if err != nil {
			writeError(w, http.StatusNotFound, "source thread root not found")
			return nil, false
		}
		if rootMsg.ThreadRootMessageID.Valid {
			writeError(w, http.StatusBadRequest, "source thread root must be a root message")
			return nil, false
		}
	}

	attachmentIDs := uuidStrings(h.channelMessageAttachmentIDs(r.Context(), parseUUID(workspaceID), channelID, rootID, messageID))
	summary := h.buildQuickCreateSourceSummary(r.Context(), parseUUID(workspaceID), channelID, channelKind, channelName, rootID)
	source := protocol.QuickCreateSourceContext{
		ChannelID:           uuidToString(channelID),
		ChannelKind:         channelKind,
		ThreadRootMessageID: uuidToString(rootID),
		SourceMessageID:     uuidToString(sourceMsg.ID),
		SourceAuthorType:    sourceMsg.AuthorType,
		SourceAuthorName:    sourceMsg.AuthorName,
		SourceExcerpt:       truncateChannelHistoryContent(sourceMsg.Content),
		Summary:             summary,
		AttachmentIDs:       attachmentIDs,
	}
	if channelKind == "group" {
		source.ChannelName = channelName
	}
	if sourceMsg.AuthorID.Valid {
		source.SourceAuthorID = uuidToString(sourceMsg.AuthorID)
	}
	return &source, true
}

func (h *Handler) loadQuickCreateSourceMessage(ctx context.Context, workspaceID, channelID, messageID pgtype.UUID) (quickCreateSourceMessage, error) {
	var msg quickCreateSourceMessage
	err := h.DB.QueryRow(ctx, `
		SELECT id, author_type, author_id, author_name, content, thread_root_message_id, thread_id, created_at
		FROM channel_message
		WHERE id = $1
		  AND channel_id = $2
		  AND workspace_id = $3
		  AND deleted_at IS NULL`, messageID, channelID, workspaceID).
		Scan(&msg.ID, &msg.AuthorType, &msg.AuthorID, &msg.AuthorName, &msg.Content, &msg.ThreadRootMessageID, &msg.ThreadID, &msg.CreatedAt)
	return msg, err
}

// channelMessageAttachmentIDs returns distinct attachment UUIDs referenced by
// any of the given channel messages. Used by quick-create source context and
// by CreateIssue auto-bind so chat→issue conversion cannot drop reference
// images when the agent forgets --attachment-id (LRM-731).
func (h *Handler) channelMessageAttachmentIDs(ctx context.Context, workspaceID, channelID pgtype.UUID, messageIDs ...pgtype.UUID) []pgtype.UUID {
	ids := make([]pgtype.UUID, 0, len(messageIDs))
	seen := make(map[string]struct{}, len(messageIDs))
	for _, id := range messageIDs {
		if !id.Valid {
			continue
		}
		key := uuidToString(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT DISTINCT attachment.id
		FROM channel_message_attachment reference
		JOIN channel_message message
		  ON message.workspace_id = reference.workspace_id
		 AND message.id = reference.channel_message_id
		JOIN attachment
		  ON attachment.workspace_id = reference.workspace_id
		 AND attachment.id = reference.attachment_id
		WHERE reference.workspace_id = $1
		  AND message.channel_id = $2
		  AND reference.channel_message_id = ANY($3::uuid[])
		ORDER BY attachment.id`, workspaceID, channelID, ids)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err == nil && id.Valid {
			out = append(out, id)
		}
	}
	return out
}

func uuidStrings(ids []pgtype.UUID) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id.Valid {
			out = append(out, uuidToString(id))
		}
	}
	return out
}

func (h *Handler) buildQuickCreateSourceSummary(ctx context.Context, workspaceID, channelID pgtype.UUID, channelKind, channelName string, rootID pgtype.UUID) string {
	rows, err := h.DB.Query(ctx, `
		SELECT id, author_type, author_name, content, created_at
		FROM channel_message
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND deleted_at IS NULL
		  AND (id = $3 OR thread_root_message_id = $3)
		ORDER BY seq DESC
		LIMIT $4`, channelID, workspaceID, rootID, quickCreateSourceMessageLimit)
	if err != nil {
		return ""
	}
	defer rows.Close()

	type summaryLine struct {
		id         pgtype.UUID
		authorType string
		authorName string
		content    string
		createdAt  pgtype.Timestamptz
	}
	var reversed []summaryLine
	for rows.Next() {
		var line summaryLine
		if err := rows.Scan(&line.id, &line.authorType, &line.authorName, &line.content, &line.createdAt); err == nil {
			reversed = append(reversed, line)
		}
	}
	var b strings.Builder
	if channelKind == "dm" {
		b.WriteString("Source surface: DM thread.\n")
	} else if strings.TrimSpace(channelName) != "" {
		fmt.Fprintf(&b, "Source surface: channel #%s thread.\n", channelName)
	} else {
		b.WriteString("Source surface: channel thread.\n")
	}
	fmt.Fprintf(&b, "Thread root message: %s.\n", uuidToString(rootID))
	if len(reversed) == 0 {
		return b.String()
	}
	b.WriteString("Recent visible messages from the source thread:\n")
	for i := len(reversed) - 1; i >= 0; i-- {
		line := reversed[i]
		created := timestampToString(line.createdAt)
		fmt.Fprintf(&b, "- [%s] %s (%s, %s): %s\n", created, line.authorName, line.authorType, uuidToString(line.id), truncateChannelHistoryContent(line.content))
	}
	return strings.TrimSpace(b.String())
}
