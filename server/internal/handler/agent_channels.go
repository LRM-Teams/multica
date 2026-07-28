package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// AgentChannelListResponse is the agent data-plane channel list (slice1).
// Only channels where the agent is a current channel_member (type=agent).
type AgentChannelListItem struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Kind        string  `json:"kind"`
	ArchivedAt  *string `json:"archived_at,omitempty"`
}

// ListAgentChannels — GET /api/agent/channels
// Auth: AgentPrincipal only. Never lists owner-only channels.
func (h *Handler) ListAgentChannels(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	agentID, ok := p.AgentUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	wsID := parseUUID(p.WorkspaceID)
	archivedOnly := queryBool(r, "archived")

	rows, err := h.DB.Query(r.Context(), `
		SELECT ch.id, ch.workspace_id, ch.name, ch.description, ch.kind, ch.archived_at
		FROM channel ch
		JOIN channel_member cm
		  ON cm.channel_id = ch.id
		 AND cm.workspace_id = ch.workspace_id
		 AND cm.member_type = 'agent'
		 AND cm.member_id = $2
		WHERE ch.workspace_id = $1
		  AND ch.kind = 'group'
		  AND (($3 AND ch.archived_at IS NOT NULL) OR (NOT $3 AND ch.archived_at IS NULL))
		ORDER BY ch.updated_at DESC, ch.created_at DESC`,
		wsID, agentID, archivedOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	defer rows.Close()

	out := []AgentChannelListItem{}
	for rows.Next() {
		var id, workspaceID pgtype.UUID
		var name, kind string
		var desc pgtype.Text
		var archivedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &workspaceID, &name, &desc, &kind, &archivedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channels")
			return
		}
		item := AgentChannelListItem{
			ID:          uuidToString(id),
			WorkspaceID: uuidToString(workspaceID),
			Name:        name,
			Description: textToPtr(desc),
			Kind:        kind,
			ArchivedAt:  timestampToPtr(archivedAt),
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

// ListAgentChannelMembers — GET /api/agent/channels/{channelId}/members
func (h *Handler) ListAgentChannelMembers(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireAgentSurfaceAccessHTTP(w, r, p, channelID) {
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT cm.member_type, cm.member_id,
		       COALESCE(u.name, a.name, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, ''),
		       CASE WHEN cm.member_type = 'user' THEN u.avatar_url ELSE a.avatar_url END,
		       cs.runtime_token_stats,
		       cm.created_at
		FROM channel_member cm
		LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
		LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		LEFT JOIN channel_agent_session cas ON cm.member_type = 'agent' AND cas.channel_id = cm.channel_id AND cas.agent_id = cm.member_id
		LEFT JOIN chat_session cs ON cs.id = cas.chat_session_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2
		ORDER BY cm.created_at ASC`, channelID, parseUUID(p.WorkspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel members")
		return
	}
	defer rows.Close()
	out := []ChannelMemberResponse{}
	for rows.Next() {
		var typ, name, displayName string
		var id pgtype.UUID
		var avatarURL pgtype.Text
		var runtimeStatsRaw []byte
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&typ, &id, &name, &displayName, &avatarURL, &runtimeStatsRaw, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel members")
			return
		}
		member := ChannelMemberResponse{
			MemberType:  typ,
			MemberID:    uuidToString(id),
			Name:        name,
			DisplayName: firstNonEmpty(displayName, name),
			AvatarURL:   textToPtr(avatarURL),
			CreatedAt:   timestampToString(createdAt),
		}
		if len(runtimeStatsRaw) > 0 {
			var stats protocol.RuntimeTokenStats
			if json.Unmarshal(runtimeStatsRaw, &stats) == nil {
				member.RuntimeStats = &stats
			}
		}
		out = append(out, member)
	}
	writeJSON(w, http.StatusOK, out)
}

// MuteAgentChannel — PUT /api/agent/channels/{channelId}/mute
func (h *Handler) MuteAgentChannel(w http.ResponseWriter, r *http.Request) {
	h.setAgentChannelMuted(w, r, true)
}

// UnmuteAgentChannel — DELETE /api/agent/channels/{channelId}/mute
func (h *Handler) UnmuteAgentChannel(w http.ResponseWriter, r *http.Request) {
	h.setAgentChannelMuted(w, r, false)
}

func (h *Handler) setAgentChannelMuted(w http.ResponseWriter, r *http.Request, muted bool) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireAgentSurfaceAccessHTTP(w, r, p, channelID) {
		return
	}
	agentUUID, ok := p.AgentUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	wsUUID := parseUUID(p.WorkspaceID)

	_, err := h.DB.Exec(r.Context(), `
		WITH updated_cm AS (
		  UPDATE channel_member
		  SET muted_at = CASE WHEN $4 THEN now() ELSE NULL END
		  WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3
		  RETURNING channel_id, workspace_id, member_id, muted_at
		),
		conv AS (
		  SELECT id FROM conversation WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET muted_at = updated_cm.muted_at, updated_at = now()
		FROM updated_cm, conv
		WHERE cm.conversation_id = conv.id
		  AND cm.workspace_id = updated_cm.workspace_id
		  AND cm.member_type = 'agent'
		  AND cm.member_id = updated_cm.member_id`,
		channelID, wsUUID, agentUUID, muted)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agent mute state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "muted": muted})
}
