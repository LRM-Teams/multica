package service

import (
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DaemonConnected reports whether the physical machine (daemon) itself is
// reachable, derived from the daemon's own heartbeat freshness — never from
// aggregating the last_seen_at of the agent_runtime rows it hosts. Those
// answer a different question ("is some runtime on this machine alive") and
// disagree with real connectivity whenever the daemon is up but hosts no
// live runtime (task #58, 2026-08-01: the s144 "Active now / Offline"
// contradiction). hb is nil when the daemon has never sent a heartbeat.
//
// Moved here from handler.computerConnected (task #58) so service-package
// dispatch-admission code (task #50) can use the same daemon-liveness
// judgment without handler importing service in reverse. handler package
// keeps a thin alias to this.
func DaemonConnected(hb *db.DaemonHeartbeat, now time.Time) bool {
	if hb == nil || !hb.LastSeenAt.Valid {
		return false
	}
	return now.Sub(hb.LastSeenAt.Time) <= AgentHealthStaleThreshold
}
