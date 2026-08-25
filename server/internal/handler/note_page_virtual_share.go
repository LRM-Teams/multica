package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type noteShareRoster struct {
	UserIDs    []string
	AgentIDs   []string
	ChannelIDs []string
}

func (h *Handler) noteShareRoster(ctx context.Context, pageID pgtype.UUID) (noteShareRoster, error) {
	users, err := h.noteShareUserIDs(ctx, pageID)
	if err != nil {
		return noteShareRoster{}, err
	}
	agents, err := h.noteShareIDList(ctx, `SELECT agent_id FROM note_page_share_agent WHERE page_id = $1 ORDER BY created_at ASC`, pageID)
	if err != nil {
		return noteShareRoster{}, err
	}
	channels, err := h.noteShareIDList(ctx, `SELECT channel_id FROM note_page_share_channel WHERE page_id = $1 ORDER BY created_at ASC`, pageID)
	if err != nil {
		return noteShareRoster{}, err
	}
	return noteShareRoster{UserIDs: users, AgentIDs: agents, ChannelIDs: channels}, nil
}

func (h *Handler) noteShareIDList(ctx context.Context, query string, pageID pgtype.UUID) ([]string, error) {
	rows, err := h.DB.Query(ctx, query, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, uuidToString(id))
	}
	return ids, rows.Err()
}

func applyNoteShareRoster(resp *NotePageResponse, roster noteShareRoster) {
	resp.ShareUserIDs = roster.UserIDs
	resp.ShareAgentIDs = roster.AgentIDs
	resp.ShareChannelIDs = roster.ChannelIDs
}

func (h *Handler) attachNoteShareRoster(ctx context.Context, resp *NotePageResponse, pageID pgtype.UUID) error {
	roster, err := h.noteShareRoster(ctx, pageID)
	if err != nil {
		return err
	}
	applyNoteShareRoster(resp, roster)
	return nil
}

func (h *Handler) notePageResponseWithShares(ctx context.Context, page notePageRow, userID pgtype.UUID, refs []NotePageIssueRefResponse) (NotePageResponse, error) {
	resp := notePageToResponse(page, userID, []string{}, refs)
	if err := h.attachNoteShareRoster(ctx, &resp, page.ID); err != nil {
		return NotePageResponse{}, err
	}
	return resp, nil
}

func stringSet(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

func addedShareIDs(previous, next []string) []string {
	had := stringSet(previous)
	added := make([]string, 0)
	for _, id := range next {
		if _, ok := had[id]; !ok {
			added = append(added, id)
		}
	}
	return added
}

func (h *Handler) replaceNotePageShares(
	ctx context.Context,
	tx pgx.Tx,
	w http.ResponseWriter,
	page notePageRow,
	ownerID pgtype.UUID,
	req noteShareRequest,
) (noteShareRoster, bool) {
	if _, err := tx.Exec(ctx, `DELETE FROM note_page_share WHERE page_id = $1`, page.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update shares")
		return noteShareRoster{}, false
	}
	if _, err := tx.Exec(ctx, `DELETE FROM note_page_share_agent WHERE page_id = $1`, page.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update shares")
		return noteShareRoster{}, false
	}
	if _, err := tx.Exec(ctx, `DELETE FROM note_page_share_channel WHERE page_id = $1`, page.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update shares")
		return noteShareRoster{}, false
	}

	users := []string{}
	seenUsers := map[string]bool{}
	for _, rawID := range req.UserIDs {
		targetID, ok := parseUUIDOrBadRequest(w, rawID, "share user id")
		if !ok {
			return noteShareRoster{}, false
		}
		key := uuidToString(targetID)
		if key == uuidToString(ownerID) || seenUsers[key] {
			continue
		}
		seenUsers[key] = true
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM member WHERE workspace_id = $1 AND user_id = $2)`, page.WorkspaceID, targetID).Scan(&exists); err != nil || !exists {
			writeError(w, http.StatusBadRequest, "share user must be a workspace member")
			return noteShareRoster{}, false
		}
		if _, err := tx.Exec(ctx, `INSERT INTO note_page_share (page_id, user_id, created_by) VALUES ($1, $2, $3)`, page.ID, targetID, ownerID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update shares")
			return noteShareRoster{}, false
		}
		users = append(users, key)
	}

	agents := []string{}
	seenAgents := map[string]bool{}
	for _, rawID := range req.AgentIDs {
		targetID, ok := parseUUIDOrBadRequest(w, rawID, "share agent id")
		if !ok {
			return noteShareRoster{}, false
		}
		key := uuidToString(targetID)
		if seenAgents[key] {
			continue
		}
		seenAgents[key] = true
		var exists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM agent
  WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL
)`, targetID, page.WorkspaceID).Scan(&exists); err != nil || !exists {
			writeError(w, http.StatusBadRequest, "share agent must be a live workspace agent")
			return noteShareRoster{}, false
		}
		if _, err := tx.Exec(ctx, `INSERT INTO note_page_share_agent (page_id, agent_id, created_by) VALUES ($1, $2, $3)`, page.ID, targetID, ownerID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update shares")
			return noteShareRoster{}, false
		}
		agents = append(agents, key)
	}

	channels := []string{}
	seenChannels := map[string]bool{}
	for _, rawID := range req.ChannelIDs {
		targetID, ok := parseUUIDOrBadRequest(w, rawID, "share channel id")
		if !ok {
			return noteShareRoster{}, false
		}
		key := uuidToString(targetID)
		if seenChannels[key] {
			continue
		}
		seenChannels[key] = true
		var exists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM channel c
  JOIN channel_member cm ON cm.channel_id = c.id
    AND cm.workspace_id = c.workspace_id
    AND cm.member_type = 'user'
    AND cm.member_id = $3
  WHERE c.id = $1
    AND c.workspace_id = $2
    AND c.kind = 'group'
    AND c.archived_at IS NULL
)`, targetID, page.WorkspaceID, ownerID).Scan(&exists); err != nil || !exists {
			writeError(w, http.StatusBadRequest, "share channel must be a group the owner belongs to")
			return noteShareRoster{}, false
		}
		if _, err := tx.Exec(ctx, `INSERT INTO note_page_share_channel (page_id, channel_id, created_by) VALUES ($1, $2, $3)`, page.ID, targetID, ownerID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update shares")
			return noteShareRoster{}, false
		}
		channels = append(channels, key)
	}

	return noteShareRoster{UserIDs: users, AgentIDs: agents, ChannelIDs: channels}, true
}

func (h *Handler) publishNoteShareCards(
	ctx context.Context,
	page notePageRow,
	ownerID pgtype.UUID,
	addedAgents, addedChannels []string,
) {
	if len(addedAgents) == 0 && len(addedChannels) == 0 {
		return
	}
	workspaceID := uuidToString(page.WorkspaceID)
	ownerKey := uuidToString(ownerID)
	authorName := h.userName(ctx, ownerID)
	for _, agentID := range addedAgents {
		agentUUID := parseUUID(agentID)
		ch, ok := h.ensureNoteShareAgentDM(ctx, workspaceID, ownerKey, agentUUID)
		if !ok {
			slog.Warn("note share: skip agent card", "page", uuidToString(page.ID), "agent", agentID)
			continue
		}
		h.postNoteShareCard(ctx, ch, page, ownerID, authorName)
	}
	for _, channelID := range addedChannels {
		ch, err := scanChannel(h.DB.QueryRow(ctx, `
SELECT id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url
FROM channel
WHERE id = $1 AND workspace_id = $2 AND kind = 'group' AND archived_at IS NULL`, parseUUID(channelID), page.WorkspaceID))
		if err != nil {
			slog.Warn("note share: skip channel card", "page", uuidToString(page.ID), "channel", channelID, "error", err)
			continue
		}
		h.postNoteShareCard(ctx, ch, page, ownerID, authorName)
	}
}

func (h *Handler) ensureNoteShareAgentDM(
	ctx context.Context,
	workspaceID, userID string,
	agentID pgtype.UUID,
) (ChannelResponse, bool) {
	canonical := dmCanonicalName("user", userID, "agent", uuidToString(agentID))
	if ch, found := h.findDMChannel(ctx, workspaceID, canonical); found {
		h.clearDMPeerHidden(ctx, workspaceID, userID, dmPeerRef{Type: "agent", ID: agentID})
		return ch, true
	}
	ch, created := h.createDMChannel(ctx, httptest.NewRecorder(), workspaceID, userID, canonical, []dmMember{
		{memberType: "user", memberID: parseUUID(userID)},
		{memberType: "agent", memberID: agentID},
	})
	if !created {
		return ChannelResponse{}, false
	}
	h.clearDMPeerHidden(ctx, workspaceID, userID, dmPeerRef{Type: "agent", ID: agentID})
	return ch, true
}

func (h *Handler) postNoteShareCard(
	ctx context.Context,
	ch ChannelResponse,
	page notePageRow,
	authorID pgtype.UUID,
	authorName string,
) {
	title := strings.TrimSpace(page.Title)
	if title == "" {
		title = "Untitled"
	}
	brief := protocol.MessagePart{
		Type:  protocol.MessagePartTypeNoteBrief,
		RefID: uuidToString(page.ID),
		Label: title,
	}
	content, parts, err := messageparts.Normalize(title, []protocol.MessagePart{brief})
	if err != nil {
		slog.Warn("note share: normalize card", "page", uuidToString(page.ID), "channel", ch.ID, "error", err)
		return
	}
	msg, err := h.insertChannelMessageWithParts(
		ctx,
		parseUUID(ch.ID),
		parseUUID(ch.WorkspaceID),
		"user",
		authorID,
		authorName,
		content,
		parts,
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	)
	if err != nil {
		slog.Warn("note share: insert card", "page", uuidToString(page.ID), "channel", ch.ID, "error", err)
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, ch.WorkspaceID, "member", uuidToString(authorID), parseUUID(ch.ID), msg)
}
