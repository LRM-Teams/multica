package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// emitThreadUnfollowedEvent inserts a system message in the thread when
// a user (or agent) unfollows it. The message is type=system with a
// structured event part so the FE can i18n-localize the display text.
//
// #329: thread unfollow is the only attention-management action that
// emits a system notification. Channel mute/unmute is silent (no
// broadcast, visibility via profile + Activity). Re-follow emits nothing.
// Co-ship constraint: this event must be live before (or in the same
// batch as) any daemon release that wires the CLI to the agent-mute
// endpoints, so agents never go silently mute-unresponsive with no
// visible signal.
func (h *Handler) emitThreadUnfollowedEvent(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID, rootID pgtype.UUID, actorID string) {
	// Get the actor's display name for canonical content.
	var displayName string
	err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(display_name, ''), name, email, 'User')
		FROM "user" WHERE id = $1`, parseUUID(actorID)).Scan(&displayName)
	if err != nil {
		displayName = "User"
	}

	canonical := fmt.Sprintf("@%s unfollowed this thread", displayName)
	parts := []protocol.MessagePart{
		{Text: fmt.Sprintf(`{"event":"thread_unfollowed","params":{"actor_id":"%s","actor_name":"%s"}}`, actorID, displayName)},
	}

	_, err = h.insertChannelMessageWithParts(
		ctx,
		channelID,
		parseUUID(workspaceID),
		"system",
		pgtype.UUID{},
		"system",
		canonical,
		parts,
		"multica",
		nil,
		pgtype.UUID{},
		rootID,
		nil,
		0,
	)
	if err != nil {
		slog.Warn("emitThreadUnfollowedEvent: failed to insert system message", "thread", rootID.String(), "error", err)
	}
}
