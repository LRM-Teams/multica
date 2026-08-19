package handler

import "github.com/multica-ai/multica/server/internal/service"

// runnerPresence is the process-wide Workspace Runner presence source the
// handler's runtimeConnectivity read consults before falling back to
// heartbeat freshness (LRM-1571: heartbeat retirement). It is assigned once
// at startup by cmd/server/main.go (which owns the daemon WebSocket Hub);
// nil keeps every legacy heartbeat-freshness judgment unchanged, which is
// also the state in unit tests.
//
// The Handler struct already carries DaemonHub for the handful of call sites
// that have h in scope; the free runtimeConnectivity-based helpers
// (agentHealthSummary, agentRuntimeDisplayStatus, runtimeIsPickableOnline,
// runtimeShouldFetchLatestRelease, ...) span the whole package, so a single
// package-level provider keeps every read consistent instead of threading
// presence through a dozen signatures.
var runnerPresence service.RunnerPresence

// SetRunnerPresence wires the Workspace Runner presence source once at
// startup. Call it exactly once before serving; the assignment is not
// concurrency-safe by design (startup-only).
func SetRunnerPresence(p service.RunnerPresence) {
	runnerPresence = p
}
