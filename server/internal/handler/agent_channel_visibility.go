package handler

import (
	"context"
	"fmt"
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

// normalizeAgentVisibility returns the canonical visibility value or an error
// when the value is not one of workspace|private|channel. Empty input is not
// normalized here — callers that want a default must set it explicitly.
func normalizeAgentVisibility(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	switch v {
	case agentVisibilityWorkspace, agentVisibilityPrivate, agentVisibilityChannel:
		return v, nil
	default:
		return "", fmt.Errorf("visibility must be workspace, private, or channel")
	}
}

// agentChannelVisibilityBinding is the resolved visibility + optional home
// channel after applying create/update request fields. Illegal combinations
// fail loudly (LRM-238 / LRM-370) — no silent clear or fallback.
type agentChannelVisibilityBinding struct {
	Visibility    string
	HomeChannelID pgtype.UUID // Valid only when Visibility == channel
}

// resolveAgentVisibilityBinding validates visibility/home_channel_id against
// LRM-240: channel requires exactly one home channel in the same workspace;
// workspace/private forbid home_channel_id.
func (h *Handler) resolveAgentVisibilityBinding(
	ctx context.Context,
	w http.ResponseWriter,
	workspaceID string,
	visibility string,
	homeChannelID *string,
	homeChannelProvided bool,
) (agentChannelVisibilityBinding, bool) {
	vis, err := normalizeAgentVisibility(visibility)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return agentChannelVisibilityBinding{}, false
	}

	switch vis {
	case agentVisibilityChannel:
		if !homeChannelProvided || homeChannelID == nil || strings.TrimSpace(*homeChannelID) == "" {
			writeError(w, http.StatusBadRequest, "visibility=channel requires home_channel_id")
			return agentChannelVisibilityBinding{}, false
		}
		channelUUID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*homeChannelID), "home_channel_id")
		if !ok {
			return agentChannelVisibilityBinding{}, false
		}
		if !h.validateAgentHomeChannel(ctx, w, workspaceID, channelUUID) {
			return agentChannelVisibilityBinding{}, false
		}
		return agentChannelVisibilityBinding{Visibility: vis, HomeChannelID: channelUUID}, true
	default:
		if homeChannelProvided && homeChannelID != nil && strings.TrimSpace(*homeChannelID) != "" {
			writeError(w, http.StatusBadRequest, "home_channel_id is only allowed when visibility=channel")
			return agentChannelVisibilityBinding{}, false
		}
		return agentChannelVisibilityBinding{Visibility: vis}, true
	}
}

// validateAgentHomeChannel ensures the home channel exists in the workspace and
// is a group channel (DM/system surfaces are not valid homes).
func (h *Handler) validateAgentHomeChannel(ctx context.Context, w http.ResponseWriter, workspaceID string, channelID pgtype.UUID) bool {
	var kind string
	var archived pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
		SELECT kind, archived_at
		FROM channel
		WHERE id = $1 AND workspace_id = $2
	`, channelID, parseUUID(workspaceID)).Scan(&kind, &archived)
	if err != nil {
		writeError(w, http.StatusBadRequest, "home_channel_id must reference a channel in this workspace")
		return false
	}
	if archived.Valid {
		writeError(w, http.StatusBadRequest, "home_channel_id must reference an active group channel")
		return false
	}
	if kind != "group" {
		writeError(w, http.StatusBadRequest, "home_channel_id must reference a group channel")
		return false
	}
	return true
}

// applyAgentHomeChannel persists home_channel_id to match visibility. Call after
// CreateAgent/UpdateAgent so the CHECK constraint sees a consistent pair.
// visibility=channel requires a valid home; other visibilities clear the column.
func (h *Handler) applyAgentHomeChannel(ctx context.Context, agentID pgtype.UUID, binding agentChannelVisibilityBinding) error {
	if binding.Visibility == agentVisibilityChannel {
		_, err := h.DB.Exec(ctx, `
			UPDATE agent
			SET visibility = $2, home_channel_id = $3, updated_at = now()
			WHERE id = $1
		`, agentID, binding.Visibility, binding.HomeChannelID)
		return err
	}
	_, err := h.DB.Exec(ctx, `
		UPDATE agent
		SET visibility = $2, home_channel_id = NULL, updated_at = now()
		WHERE id = $1
	`, agentID, binding.Visibility)
	return err
}

// loadAgentHomeChannelID returns the agent's home_channel_id when set.
func (h *Handler) loadAgentHomeChannelID(ctx context.Context, agentID pgtype.UUID) (pgtype.UUID, bool) {
	var home pgtype.UUID
	err := h.DB.QueryRow(ctx, `SELECT home_channel_id FROM agent WHERE id = $1`, agentID).Scan(&home)
	if err != nil || !home.Valid {
		return pgtype.UUID{}, false
	}
	return home, true
}

// loadAgentHomeChannelIDs batch-loads home_channel_id for agent IDs.
func (h *Handler) loadAgentHomeChannelIDs(ctx context.Context, agentIDs []pgtype.UUID) map[string]string {
	out := map[string]string{}
	if len(agentIDs) == 0 {
		return out
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, home_channel_id
		FROM agent
		WHERE id = ANY($1::uuid[]) AND home_channel_id IS NOT NULL
	`, agentIDs)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, home pgtype.UUID
		if err := rows.Scan(&id, &home); err != nil {
			continue
		}
		out[uuidToString(id)] = uuidToString(home)
	}
	return out
}

func attachHomeChannelIDs(resps []AgentResponse, homes map[string]string) {
	for i := range resps {
		if home, ok := homes[resps[i].ID]; ok && home != "" {
			h := home
			resps[i].HomeChannelID = &h
		}
	}
}

// agentVisibleInChannelContext reports whether a channel-visibility agent
// should appear in ListAgents / invite discovery for the given channel. Non-
// channel agents always pass this gate (private filtering is separate).
func agentVisibleInChannelContext(visibility, homeChannelID, listChannelID string) bool {
	if visibility != agentVisibilityChannel {
		return true
	}
	if listChannelID == "" {
		// Workspace directory with no channel context: channel agents are
		// discoverable only inside their home channel (LRM-240).
		return false
	}
	return homeChannelID != "" && homeChannelID == listChannelID
}

// canInviteAgentToChannel enforces channel-visibility invite rules: a channel
// agent may only be newly invited into its home channel. Existing memberships
// elsewhere are not auto-removed (LRM-240).
func canInviteAgentToChannel(agent db.Agent, homeChannelID, targetChannelID string) (bool, string) {
	if agent.Visibility != agentVisibilityChannel {
		return true, ""
	}
	if homeChannelID == "" {
		return false, "channel-visibility agent is missing home_channel_id"
	}
	if homeChannelID != targetChannelID {
		return false, "channel-visibility agent can only be invited to its home channel"
	}
	return true, ""
}

// reloadAgentAfterHomeChannelRefresh reloads the agent row after applyAgentHomeChannel
// so Visibility reflects the persisted value (CreateAgent may have written a
// temporary placeholder before the home-channel bind).
func (h *Handler) reloadAgentAfterHomeChannelRefresh(ctx context.Context, agentID pgtype.UUID) (db.Agent, error) {
	return h.Queries.GetAgent(ctx, agentID)
}

// insertSafeAgentVisibility returns a visibility value that satisfies the
// agent_home_channel_visibility_check on INSERT before home_channel_id is
// applied. Channel agents are inserted as private then flipped immediately via
// applyAgentHomeChannel — callers never observe the placeholder.
func insertSafeAgentVisibility(binding agentChannelVisibilityBinding) string {
	if binding.Visibility == agentVisibilityChannel {
		return agentVisibilityPrivate
	}
	return binding.Visibility
}
