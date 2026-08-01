package handler

import (
	"context"
	"testing"
	"time"
)

// Task #53: isRuntimeOnline previously trusted the raw agent_runtime.status
// column directly, which can read "online" for up to ~180s after the
// runtime actually went silent (sweeper lag). Quick-create issue submission
// uses this to decide whether to reject immediately with "agent's runtime is
// offline" — a stale-online runtime meant the user's submission was wrongly
// accepted, then silently queued behind a runtime that was never coming
// back in time (until #50's queue-forever fix, meaning it would just sit
// there instead of failing clearly).
func TestIsRuntimeOnlineUsesHeartbeatFreshnessNotRawStatus(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	_, staleOnlineRuntimeID := createAgentHealthFixture(t, "online",
		time.Now().Add(-10*time.Minute), // stale heartbeat
		time.Now().Add(-9*time.Minute),
	)
	if got := testHandler.isRuntimeOnline(context.Background(), parseUUID(staleOnlineRuntimeID)); got {
		t.Fatalf("stale-online runtime (status column lying): isRuntimeOnline = true, want false (must key off heartbeat freshness)")
	}

	_, freshOnlineRuntimeID := createAgentHealthFixture(t, "online",
		time.Now().Add(-5*time.Second),
		time.Now().Add(-5*time.Second),
	)
	if got := testHandler.isRuntimeOnline(context.Background(), parseUUID(freshOnlineRuntimeID)); !got {
		t.Fatalf("fresh-online runtime: isRuntimeOnline = false, want true")
	}

	_, offlineRuntimeID := createAgentHealthFixture(t, "offline",
		time.Now().Add(-10*time.Minute),
		time.Now().Add(-9*time.Minute),
	)
	if got := testHandler.isRuntimeOnline(context.Background(), parseUUID(offlineRuntimeID)); got {
		t.Fatalf("explicitly-offline runtime: isRuntimeOnline = true, want false")
	}
}
