package handler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// pickAgentRuntime chooses a runtime for platform-created agents:
// prefer a heartbeat-fresh online runtime owned by the initiating user,
// then any fresh online runtime visible to that user. Returns false when
// none are pickable.
//
// Task #123 / stability L1: never trust agent_runtime.status alone — the
// column can still read "online" for ~150s after the daemon went silent
// (sweeper lag). Do not fall back to a random first visible row (that was
// a ghost-machine bind).
func (h *Handler) pickAgentRuntime(
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
	now := time.Now()
	for _, runtime := range runtimes {
		if runtime.OwnerID.Valid &&
			uuidToString(runtime.OwnerID) == uuidToString(userID) &&
			runtimeIsPickableOnline(runtime, now) {
			return runtime, true
		}
	}
	for _, runtime := range runtimes {
		if runtimeIsPickableOnline(runtime, now) {
			return runtime, true
		}
	}
	return db.AgentRuntime{}, false
}

// runtimeIsPickableOnline is the L1/L2 gate for "bind work / create agent to
// this runtime": status may still say online while last_seen is stale.
// Uses the same runtimeConnectivity helper as isRuntimeOnline / health UI
// (service.AgentHealthStaleThreshold) — no third threshold.
func runtimeIsPickableOnline(rt db.AgentRuntime, now time.Time) bool {
	return runtimeConnectivity(rt, now) == runtimeConnectivityOnline
}
