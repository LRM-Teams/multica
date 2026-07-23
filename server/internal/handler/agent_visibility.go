package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	agentVisibilityWorkspace = "workspace"
	agentVisibilityPrivate   = "private"
	agentVisibilityChannel   = "channel"
)

// validateAgentVisibilityCombo enforces LRM-240 / LRM-370 pairing:
//   - visibility ∈ {workspace, private, channel}
//   - channel requires a non-empty home_channel_id
//   - non-channel forbids home_channel_id (explicit error, no silent clear)
//
// Returns the normalized visibility and home channel UUID (may be invalid).
func validateAgentVisibilityCombo(w http.ResponseWriter, visibility string, homeChannelID *string) (string, pgtype.UUID, bool) {
	visibility = strings.TrimSpace(visibility)
	if visibility == "" {
		visibility = agentVisibilityPrivate
	}
	switch visibility {
	case agentVisibilityWorkspace, agentVisibilityPrivate, agentVisibilityChannel:
	default:
		writeError(w, http.StatusBadRequest, "visibility must be workspace, private, or channel")
		return "", pgtype.UUID{}, false
	}

	homeRaw := ""
	if homeChannelID != nil {
		homeRaw = strings.TrimSpace(*homeChannelID)
	}

	if visibility == agentVisibilityChannel {
		if homeRaw == "" {
			writeError(w, http.StatusBadRequest, "home_channel_id is required when visibility is channel")
			return "", pgtype.UUID{}, false
		}
		homeUUID, ok := parseUUIDOrBadRequest(w, homeRaw, "home_channel_id")
		if !ok {
			return "", pgtype.UUID{}, false
		}
		return visibility, homeUUID, true
	}

	if homeRaw != "" {
		writeError(w, http.StatusBadRequest, "home_channel_id is only allowed when visibility is channel")
		return "", pgtype.UUID{}, false
	}
	return visibility, pgtype.UUID{}, true
}

// validateHomeChannelInWorkspace ensures home_channel_id points at a live group
// channel in the same workspace (DM/system channels are not valid homes).
func (h *Handler) validateHomeChannelInWorkspace(w http.ResponseWriter, r *http.Request, workspaceID string, homeChannelID pgtype.UUID) bool {
	var kind string
	var archivedAt pgtype.Timestamptz
	err := h.DB.QueryRow(r.Context(), `
		SELECT kind, archived_at
		FROM channel
		WHERE id = $1 AND workspace_id = $2
	`, homeChannelID, parseUUID(workspaceID)).Scan(&kind, &archivedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "home_channel_id must reference a channel in this workspace")
		return false
	}
	if archivedAt.Valid {
		writeError(w, http.StatusBadRequest, "home_channel_id must reference an active channel")
		return false
	}
	if kind != "group" {
		writeError(w, http.StatusBadRequest, "home_channel_id must reference a group channel")
		return false
	}
	return true
}

func (h *Handler) setAgentHomeChannel(ctx context.Context, agentID, homeChannelID pgtype.UUID) error {
	if homeChannelID.Valid {
		_, err := h.DB.Exec(ctx, `
			UPDATE agent SET home_channel_id = $2, updated_at = now() WHERE id = $1
		`, agentID, homeChannelID)
		return err
	}
	_, err := h.DB.Exec(ctx, `
		UPDATE agent SET home_channel_id = NULL, updated_at = now() WHERE id = $1
	`, agentID)
	return err
}

func (h *Handler) setAgentVisibilityAndHome(ctx context.Context, agentID pgtype.UUID, visibility string, homeChannelID pgtype.UUID) error {
	_, err := h.DB.Exec(ctx, `
		UPDATE agent
		SET visibility = $2,
		    home_channel_id = $3,
		    updated_at = now()
		WHERE id = $1
	`, agentID, visibility, homeChannelID)
	return err
}

// agentHomeChannelIDs loads home_channel_id for the given agents (sqlc Agent
// omits the column; same pattern as managed_role).
func (h *Handler) agentHomeChannelIDs(ctx context.Context, agentIDs []pgtype.UUID) (map[string]string, error) {
	out := map[string]string{}
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, home_channel_id
		FROM agent
		WHERE id = ANY($1::uuid[]) AND home_channel_id IS NOT NULL
	`, agentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, home pgtype.UUID
		if err := rows.Scan(&id, &home); err != nil {
			return nil, err
		}
		out[uuidToString(id)] = uuidToString(home)
	}
	return out, rows.Err()
}

func (h *Handler) attachAgentHomeChannels(ctx context.Context, resps []AgentResponse) {
	if len(resps) == 0 {
		return
	}
	ids := make([]pgtype.UUID, 0, len(resps))
	for i := range resps {
		if id := parseUUID(resps[i].ID); id.Valid {
			ids = append(ids, id)
		}
	}
	homes, err := h.agentHomeChannelIDs(ctx, ids)
	if err != nil {
		slog.Warn("failed to load agent home_channel_id", "error", err)
		return
	}
	for i := range resps {
		if home, ok := homes[resps[i].ID]; ok && home != "" {
			resps[i].HomeChannelID = &home
		}
	}
}

// agentVisibleInList applies LRM-240 directory/invite discovery rules.
// listChannelID is the optional invite/ListAgents channel context C:
//   - visibility=channel agents appear only when channel_id == home_channel_id
//   - without channel_id they stay hidden from the workspace directory
func agentVisibleInList(a db.Agent, homeChannelID string, listChannelID string, actorType, actorID, memberRole string, groupManagers map[string]bool) bool {
	agentID := uuidToString(a.ID)
	if groupManagers[agentID] {
		// Group managers stay out of the workspace directory / invite picker
		// (LRM-233); they are channel-bound infrastructure, not invite targets.
		return false
	}
	switch a.Visibility {
	case agentVisibilityChannel:
		if listChannelID == "" || homeChannelID == "" || homeChannelID != listChannelID {
			return false
		}
		return true
	case agentVisibilityPrivate:
		if actorType == "member" {
			return memberAllowedForPrivateAgent(a, actorID, memberRole)
		}
		return true
	default:
		if actorType == "member" && privateAgentOwnerOnly(a) {
			return memberAllowedForPrivateAgent(a, actorID, memberRole)
		}
		return true
	}
}

// canInviteAgentToChannel enforces invite-panel rules for channel visibility:
// channel agents may only be newly invited into their home channel.
func canInviteAgentToChannel(agent db.Agent, homeChannelID, targetChannelID string) error {
	if agent.Visibility != agentVisibilityChannel {
		return nil
	}
	if homeChannelID == "" || homeChannelID != targetChannelID {
		return fmt.Errorf("channel-visibility agent can only be invited to its home channel")
	}
	return nil
}

// channelVisibilityAllowsMention reports whether a channel-visibility agent may
// appear in @mention candidates for the current channel (LRM-240).
func channelVisibilityAllowsMention(visibility, homeChannelID, currentChannelID string) bool {
	if visibility != agentVisibilityChannel {
		return true
	}
	return homeChannelID != "" && homeChannelID == currentChannelID
}
