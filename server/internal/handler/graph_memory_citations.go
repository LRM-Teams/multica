package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type graphMemoryCitationResponse struct {
	ID              string   `json:"id"`
	NodeID          string   `json:"node_id"`
	GraphVersion    int64    `json:"graph_version"`
	Level           string   `json:"level"`
	EpistemicStatus string   `json:"epistemic_status,omitempty"`
	Tags            []string `json:"tags"`
	Title           string   `json:"title"`
	FirstParagraph  string   `json:"first_paragraph"`
	Excerpt         string   `json:"excerpt"`
	ContentHash     string   `json:"content_hash"`
	CapturedAt      string   `json:"captured_at"`
}

type graphMemoryMessageCitationsResponse struct {
	MessageID string                        `json:"message_id"`
	Items     []graphMemoryCitationResponse `json:"items"`
}

func (h *Handler) GetGraphMemoryMessageCitations(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT citation.id::text,citation.node_id,citation.graph_version,citation.level,
		       citation.epistemic_status,citation.tags,citation.title,citation.first_paragraph,
		       citation.excerpt,citation.content_hash,citation.captured_at::text
		FROM channel_message message
		JOIN channel_member membership
		  ON membership.channel_id=message.channel_id AND membership.workspace_id=message.workspace_id
		 AND membership.member_type='user' AND membership.member_id=$3
		JOIN graph_memory_agent_citation citation ON citation.message_id=message.id
		WHERE message.id=$1 AND message.workspace_id=$2::uuid
		ORDER BY citation.captured_at,citation.id`, messageID, workspaceID, member.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load Graph Memory citations")
		return
	}
	defer rows.Close()
	items := make([]graphMemoryCitationResponse, 0)
	for rows.Next() {
		var item graphMemoryCitationResponse
		var tags json.RawMessage
		if err := rows.Scan(&item.ID, &item.NodeID, &item.GraphVersion, &item.Level, &item.EpistemicStatus,
			&tags, &item.Title, &item.FirstParagraph, &item.Excerpt, &item.ContentHash, &item.CapturedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan Graph Memory citations")
			return
		}
		_ = json.Unmarshal(tags, &item.Tags)
		if item.Tags == nil {
			item.Tags = []string{}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to iterate Graph Memory citations")
		return
	}
	if len(items) == 0 {
		var visible bool
		if err := h.DB.QueryRow(r.Context(), `
			SELECT EXISTS(
			 SELECT 1 FROM channel_message message JOIN channel_member membership
			 ON membership.channel_id=message.channel_id AND membership.workspace_id=message.workspace_id
			 AND membership.member_type='user' AND membership.member_id=$3
			 WHERE message.id=$1 AND message.workspace_id=$2::uuid
			)`, messageID, workspaceID, member.UserID).Scan(&visible); err != nil || !visible {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
	}
	writeJSON(w, http.StatusOK, graphMemoryMessageCitationsResponse{MessageID: uuidToString(messageID), Items: items})
}

// attachGraphMemoryCitationsToPublishedMessage binds the immutable snapshots
// from the latest unbound submitted trajectory to the managed Agent's first
// canonical output message. It never reads current graph content.
func (h *Handler) attachGraphMemoryCitationsToPublishedMessage(ctx context.Context, message ChannelMessageResponse) {
	if h == nil || h.DB == nil || message.Type != "agent" || message.AuthorID == nil {
		return
	}
	_, _ = h.DB.Exec(ctx, `
		WITH submitted AS (
		  SELECT trajectory.id
		  FROM graph_memory_channel_agent managed
		  JOIN graph_memory_agent_run run ON run.channel_id=managed.channel_id AND run.status='submitted'
		  JOIN graph_memory_agent_trajectory trajectory ON trajectory.run_id=run.id
		  WHERE managed.channel_id=$1::uuid AND managed.workspace_id=$2::uuid
		    AND managed.agent_id=$3::uuid
		    AND EXISTS (SELECT 1 FROM graph_memory_agent_citation c WHERE c.trajectory_id=trajectory.id AND c.message_id IS NULL)
		  ORDER BY run.finished_at DESC
		  LIMIT 1
		)
		UPDATE graph_memory_agent_citation citation SET message_id=$4::uuid
		FROM submitted WHERE citation.trajectory_id=submitted.id AND citation.message_id IS NULL`,
		message.ChannelID, message.WorkspaceID, *message.AuthorID, message.ID)
}
