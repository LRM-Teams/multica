package service

import (
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// AgentHealthStaleThreshold mirrors the runtime sweeper's stale
	// threshold (150s in cmd/server/runtime_sweeper.go). Even if the DB row
	// still says "online", a heartbeat older than this means the runtime is
	// effectively unreachable and must not be treated as online.
	AgentHealthStaleThreshold = 150 * time.Second
	// AgentHealthReconnectAfter escalates a stale runtime to "dead" once the
	// heartbeat gap is this large.
	AgentHealthReconnectAfter = 5 * time.Minute
)

// RuntimeConnectivityTier is a read-time judgment of whether a runtime's
// heartbeat is currently trustworthy. It is never written back to the
// database — agent_runtime.status stays the sweeper's job (~150s+sweep
// interval to flip offline) because dispatch/admission logic depends on
// that column being stable and cheap to read. This tier exists purely to
// stop callers (across both the service and handler packages — task #53
// consolidated what used to be two independent copies) from trusting the
// raw column once it's known to be stale.
type RuntimeConnectivityTier int

const (
	RuntimeConnectivityOnline RuntimeConnectivityTier = iota
	RuntimeConnectivityStale
	RuntimeConnectivityDead
)

// RuntimeConnectivity applies the freshness gate this package and the
// handler package's Activity Health tab have used since #284: even if the
// DB row still says "online", a heartbeat older than AgentHealthStaleThreshold
// means the runtime is not actually reachable right now. A row already
// persisted "offline" by the sweeper gets the same tiering, just keyed off
// UpdatedAt (the sweeper's flip time) instead of LastSeenAt.
func RuntimeConnectivity(rt db.AgentRuntime, now time.Time) RuntimeConnectivityTier {
	if rt.LastSeenAt.Valid && now.Sub(rt.LastSeenAt.Time) >= AgentHealthStaleThreshold {
		if now.Sub(rt.LastSeenAt.Time) >= AgentHealthReconnectAfter {
			return RuntimeConnectivityDead
		}
		return RuntimeConnectivityStale
	}
	if rt.Status == "offline" {
		if rt.UpdatedAt.Valid && now.Sub(rt.UpdatedAt.Time) >= AgentHealthReconnectAfter {
			return RuntimeConnectivityDead
		}
		return RuntimeConnectivityStale
	}
	return RuntimeConnectivityOnline
}
