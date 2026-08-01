package handler

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// computerConnected reports whether the physical machine (daemon) itself is
// reachable, derived from the daemon's own heartbeat freshness — never from
// aggregating the last_seen_at of the agent_runtime rows it hosts. Those
// answer a different question ("is some runtime on this machine alive") and
// disagree with real connectivity whenever the daemon is up but hosts no
// live runtime (task #58, 2026-08-01: the s144 "Active now / Offline"
// contradiction). hb is nil when the daemon has never sent a heartbeat.
func computerConnected(hb *db.DaemonHeartbeat, now time.Time) bool {
	if hb == nil || !hb.LastSeenAt.Valid {
		return false
	}
	return now.Sub(hb.LastSeenAt.Time) <= service.AgentHealthStaleThreshold
}

// daemonHeartbeatsForList bulk-fetches one daemon_heartbeat row per distinct
// daemon_id referenced by runtimes, mirroring daemonUpdateStatusesForList's
// shape so a list response never issues one query per runtime.
func (h *Handler) daemonHeartbeatsForList(ctx context.Context, runtimes []db.AgentRuntime) map[string]*db.DaemonHeartbeat {
	result := make(map[string]*db.DaemonHeartbeat)
	if h == nil || h.Queries == nil || len(runtimes) == 0 {
		return result
	}
	var workspaceID = runtimes[0].WorkspaceID

	rows, err := h.Queries.GetDaemonHeartbeatsForWorkspace(ctx, workspaceID)
	if err != nil {
		return result
	}
	for i := range rows {
		row := rows[i]
		result[row.DaemonID] = &row
	}
	return result
}
