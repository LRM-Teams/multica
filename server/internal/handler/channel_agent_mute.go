package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// MuteChannelAgent sets the agent's muted_at for a channel, suppressing
// ambient wake. Called by `multica channel mute` when the CLI runs as an agent.
func (h *Handler) MuteChannelAgent(w http.ResponseWriter, r *http.Request) {
	h.setChannelAgentMuted(w, r, true)
}

// UnmuteChannelAgent clears the agent's muted_at for a channel, restoring
// ambient wake. Called by `multica channel unmute` when the CLI runs as an agent.
func (h *Handler) UnmuteChannelAgent(w http.ResponseWriter, r *http.Request) {
	h.setChannelAgentMuted(w, r, false)
}

func (h *Handler) setChannelAgentMuted(w http.ResponseWriter, r *http.Request, muted bool) {
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}

	// Only agents may call this endpoint.
	actorType, actorID := h.resolveActor(r, "", workspaceID)
	if actorType != "agent" {
		writeError(w, http.StatusForbidden, "only agents can use this endpoint")
		return
	}
	agentUUID := parseUUID(actorID)
	if !agentUUID.Valid {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	// Verify agent is a member of this channel.
	var exists bool
	err := h.DB.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM channel_member
			WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3
		)`, channelID, parseUUID(workspaceID), agentUUID).Scan(&exists)
	if err != nil || !exists {
		writeError(w, http.StatusNotFound, "agent is not a member of this channel")
		return
	}

	// Update both channel_member and conversation_member so sidebar and
	// ambient gate read the same state.
	_, err = h.DB.Exec(r.Context(), `
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
		channelID, parseUUID(workspaceID), agentUUID, muted)
	if err != nil {
		slog.Error("agent mute: failed to update mute state", "channel", channelID.String(), "agent", actorID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update agent mute state")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "muted": muted})
}

// isChannelAgentMuted returns true if the agent has muted this channel.
// Called by the ambient dispatch path before enqueueing an observation task.
//
// Intentional fail-open: when the DB query fails, we return false (not muted)
// so the ambient dispatch proceeds. The alternative (fail-closed) would cause
// ambient messages to be silently dropped — exactly the kind of "no one knows
// why I didn't get a reply" bug that #310/#304 fought. Mute is best-effort
// suppression; delivery guarantee wins on error.
func (h *Handler) isChannelAgentMuted(ctx context.Context, channelID, workspaceID, agentID pgtype.UUID) bool {
	var mutedAt pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
		SELECT muted_at FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3
		  AND muted_at IS NOT NULL`,
		channelID, workspaceID, agentID).Scan(&mutedAt)
	if err != nil {
		return false
	}
	return mutedAt.Valid
}
