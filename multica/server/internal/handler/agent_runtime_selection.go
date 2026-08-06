package handler

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// pickVisibleAgentRuntime chooses a runtime for platform-created agents:
// prefer an online runtime owned by the initiating user, then any online
// runtime, then the first visible runtime.
func (h *Handler) pickVisibleAgentRuntime(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
) (db.AgentRuntime, bool) {
	runtimes, err := h.Queries.ListVisibleAgentRuntimes(ctx, db.ListVisibleAgentRuntimesParams{
		WorkspaceID: workspaceID,
		OwnerID:     userID,
	})
	if err != nil || len(runtimes) == 0 {
		return db.AgentRuntime{}, false
	}
	for _, runtime := range runtimes {
		if runtime.OwnerID.Valid &&
			uuidToString(runtime.OwnerID) == uuidToString(userID) &&
			runtime.Status == "online" {
			return runtime, true
		}
	}
	for _, runtime := range runtimes {
		if runtime.Status == "online" {
			return runtime, true
		}
	}
	return runtimes[0], true
}
