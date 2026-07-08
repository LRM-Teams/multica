package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
)

// isWorkspaceOwnerOrAdmin checks whether the given user has the 'owner'
// or 'admin' role in the workspace. Used by MarkChannelRead when the
// rewind=true flag is set — only workspace owners/admins are allowed to
// bypass the GREATEST monotonic guard for testing purposes.
func (h *Handler) isWorkspaceOwnerOrAdmin(ctx context.Context, workspaceID string, userID pgtype.UUID) bool {
	var role string
	err := h.DB.QueryRow(ctx, `
		SELECT wsm.role
		FROM workspace_member wsm
		WHERE wsm.workspace_id = $1 AND wsm.user_id = $2`,
		parseUUID(workspaceID), userID).Scan(&role)
	if err != nil {
		slog.Warn("rewind: failed to check workspace role", "error", err)
		return false
	}
	return role == "owner" || role == "admin"
}

// rewindChannelRead forcibly sets last_read_seq to an arbitrary value in
// both channel_read and conversation_member, bypassing the GREATEST
// monotonic guard. Only reachable via MarkChannelRead with rewind=true
// + workspace owner/admin role. Intended for staging/testing use only.
func (h *Handler) rewindChannelRead(w http.ResponseWriter, r *http.Request, workspaceID string, channelID, userID pgtype.UUID, toSeq int64) {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	_, err = tx.Exec(r.Context(), `
		INSERT INTO channel_read (channel_id, user_id, last_read_at, last_read_seq)
		VALUES ($1, $2, now(), $3)
		ON CONFLICT (channel_id, user_id)
		DO UPDATE SET last_read_at = now(), last_read_seq = $3`,
		channelID, userID, toSeq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel_read")
		return
	}

	_, err = tx.Exec(r.Context(), `
		INSERT INTO conversation_member (conversation_id, workspace_id, member_type, member_id, last_read_seq, followed_at, updated_at)
		SELECT conv.id, conv.workspace_id, 'user', $2, $3, now(), now()
		FROM conversation conv
		WHERE conv.channel_id = $1
		ON CONFLICT (conversation_id, member_type, member_id)
		DO UPDATE SET last_read_seq = $3, updated_at = now()`,
		channelID, userID, toSeq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update conversation_member")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"rewound": true,
		"new_seq": toSeq,
	})
}
