package service

// RunnerPresence is the "is this Computer's WorkspaceDaemon socket live
// right now" judgment used across the server for WS-authoritative liveness.
//
// LRM-1571: heartbeat retirement — for WS-capable daemons the Workspace
// Runner socket is the liveness source; HTTP heartbeat freshness is only the
// fallback for legacy daemons (no Hub / test composition). Consumers that
// have access to the daemon WebSocket Hub (sweeper, handler) consult this
// first and only degrade to the last_seen_at based RuntimeConnectivity
// read when the presence source is unavailable.
type RunnerPresence interface {
	// HasWorkspaceDaemon reports whether the daemon currently holds a live
	// DaemonCore / WorkspaceDaemon socket for a workspace. Connect is online;
	// disconnect is offline.
	HasWorkspaceDaemon(daemonID, workspaceID string) bool
}
