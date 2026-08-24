package handler

import (
	"context"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// computerConnected is a thin alias to service.DaemonConnected — moved there
// (task #50) so service-package dispatch-admission code can share the same
// daemon-liveness judgment instead of handler importing service in reverse.
func computerConnected(hb *db.DaemonHeartbeat, now time.Time) bool {
	return service.DaemonConnected(hb, now)
}

// computerConnectedByRunner is the one Computer liveness decision for both
// Computer list and runtime projections. A current DaemonCore Workspace Runner
// socket is authoritative; HTTP heartbeat freshness is only the fallback when
// Hub or identity is unavailable (legacy / test composition).
func (h *Handler) computerConnectedByRunner(daemonID, workspaceID string, hb *db.DaemonHeartbeat, now time.Time) bool {
	if h != nil && h.DaemonHub != nil && strings.TrimSpace(daemonID) != "" && strings.TrimSpace(workspaceID) != "" {
		return h.DaemonHub.HasWorkspaceDaemon(daemonID, workspaceID)
	}
	// TODO(computer-liveness): Remove after v0.4.24-alpha.55 is no
	// longer a supported direct self-upgrade source.
	return computerConnected(hb, now)
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
