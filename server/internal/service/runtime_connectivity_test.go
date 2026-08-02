package service

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestRuntimeConnectivity_OfflineReasonBypassesTimeRamp is the regression
// guard for the agent intentional-stop signal (task ①, design doc
// 2026-08-02-agent-intentional-stop-signal-design.md): a runtime that was
// confirmedly deregistered (offline_reason set) must report
// RuntimeConnectivityStopped immediately, not ride the Stale→Dead time ramp
// that a silent/unexplained offline flip goes through. UpdatedAt is only 1
// second old here specifically to prove the reason check runs BEFORE, not
// after, the time gate — without the short-circuit this would return
// RuntimeConnectivityOnline (fresh LastSeenAt) or RuntimeConnectivityStale,
// never Stopped.
func TestRuntimeConnectivity_OfflineReasonBypassesTimeRamp(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:        "offline",
		LastSeenAt:    pgtype.Timestamptz{Time: now.Add(-1 * time.Second), Valid: true},
		UpdatedAt:     pgtype.Timestamptz{Time: now.Add(-1 * time.Second), Valid: true},
		OfflineReason: pgtype.Text{String: "daemon_deregistered", Valid: true},
	}

	got := RuntimeConnectivity(rt, now)
	if got != RuntimeConnectivityStopped {
		t.Fatalf("RuntimeConnectivity() = %v, want RuntimeConnectivityStopped", got)
	}
}

// TestRuntimeConnectivity_NullOfflineReasonKeepsTimeRamp confirms the
// existing silence-based behavior is unchanged for a runtime with no known
// reason (the sweeper's path): a freshly-flipped offline row with no reason
// must NOT be reported as Stopped, only Stale/Dead per the pre-existing
// time-based logic.
func TestRuntimeConnectivity_NullOfflineReasonKeepsTimeRamp(t *testing.T) {
	now := time.Now()
	rt := db.AgentRuntime{
		Status:     "offline",
		LastSeenAt: pgtype.Timestamptz{Time: now.Add(-1 * time.Second), Valid: true},
		UpdatedAt:  pgtype.Timestamptz{Time: now.Add(-1 * time.Second), Valid: true},
		// OfflineReason left zero-value (NULL).
	}

	got := RuntimeConnectivity(rt, now)
	if got == RuntimeConnectivityStopped {
		t.Fatalf("RuntimeConnectivity() = %v, want anything but Stopped when offline_reason is NULL", got)
	}
}
