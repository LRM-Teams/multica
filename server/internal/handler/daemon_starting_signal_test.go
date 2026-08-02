package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestDaemonMarkStarting_SetsStartingSinceOnExistingRuntime is the "confirm
// it does something" half of the starting-signal feature: an already
// registered runtime (the daemon-restart case this exists for) gets
// starting_since set by a bare /api/daemon/starting call, before any
// full register.
func TestDaemonMarkStarting_SetsStartingSinceOnExistingRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "starting-signal-" + uuid.NewString()
	runtimeID := seedMachineLockedRuntime(t, daemonID, "Starting Signal Runtime")

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/daemon/starting", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
	})
	testHandler.DaemonMarkStarting(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonMarkStarting status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var startingSince *time.Time
	if err := testPool.QueryRow(context.Background(), `SELECT starting_since FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&startingSince); err != nil {
		t.Fatalf("read starting_since: %v", err)
	}
	if startingSince == nil {
		t.Fatal("starting_since is NULL after DaemonMarkStarting, want set")
	}
	// Tolerance is wide on both sides: startingSince was written by
	// Postgres's now() and compared here against the Go process's
	// time.Now() — two independent clock sources that can disagree by a
	// few ms even on the same host (test DB commonly runs in a
	// container). A tight bound here would be exactly the cross-clock
	// comparison bug task #80 fixed elsewhere; this only needs to confirm
	// "set moments ago," not race the clock.
	if since := time.Since(*startingSince); since < -5*time.Second || since > 15*time.Second {
		t.Fatalf("starting_since = %v ago, want roughly just now", since)
	}
}

// TestDaemonMarkStarting_NoopWhenNoMatchingRuntime pins decision ① from the
// design: a daemon_id with no prior runtime row (first-ever registration)
// has nothing to mark "starting" — the call must succeed as a no-op, not
// error, so a brand-new daemon's best-effort call never gets logged as a
// failure.
func TestDaemonMarkStarting_NoopWhenNoMatchingRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/daemon/starting", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    "never-registered-" + uuid.NewString(),
	})
	testHandler.DaemonMarkStarting(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonMarkStarting on unknown daemon_id status = %d, want 200 (no-op): %s", w.Code, w.Body.String())
	}
}

// TestDaemonRegister_ClearsStartingSince proves the write-side half of the
// TTL-safety story: a completed register is the authoritative "no longer
// starting" fact and must clear any starting_since left over from a prior
// MarkAgentRuntimesStarting call, not just let the read-side TTL expire it
// eventually.
func TestDaemonRegister_ClearsStartingSince(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "starting-signal-clear-" + uuid.NewString()
	runtimeID := seedMachineLockedRuntime(t, daemonID, "Starting Signal Clear Runtime")

	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET starting_since = now(), provider = 'starting_clear_test' WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("seed starting_since: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "starting-clear-device",
		"cli_version":  "v0.3.0",
		"capabilities": []string{},
		"runtimes": []map[string]any{
			{"name": "starting-clear-runtime", "type": "starting_clear_test", "version": "1.0.0", "status": "online"},
		},
	}, testWorkspaceID, daemonID)
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var startingSince *time.Time
	if err := testPool.QueryRow(context.Background(), `SELECT starting_since FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&startingSince); err != nil {
		t.Fatalf("read starting_since after register: %v", err)
	}
	if startingSince != nil {
		t.Fatalf("starting_since = %v after a completed register, want NULL", *startingSince)
	}
}

// TestAttachAgentRuntimeNames_StartingRuntimeShowsStarting is the wiring
// check for the primary agent-list/detail endpoint (GET /agents, GetAgent),
// which goes through attachAgentRuntimeNames's own hand-rolled raw-SQL query
// rather than a sqlc-generated `SELECT *`. That query originally omitted
// starting_since, so a machine mid-restart would silently show its stale
// pre-crash connectivity tier on the one surface most users actually look
// at, even though the pure agentRuntimeDisplayStatus function and its unit
// tests already handled it correctly (found independently by Alice and Vera
// during review — same shape as #1801's offline_reason miss on this exact
// query).
func TestAttachAgentRuntimeNames_StartingRuntimeShowsStarting(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// Stale-by-connectivity-alone (long past LastSeenAt/UpdatedAt) so a pass
	// here can only be explained by starting_since being read, not by an
	// otherwise-fresh row coincidentally looking fine.
	agentID, runtimeID := createAgentHealthFixture(t, "online", time.Now().Add(-10*time.Minute), time.Now().Add(-10*time.Minute))
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime SET starting_since = now() WHERE id = $1
	`, runtimeID); err != nil {
		t.Fatalf("seed starting_since: %v", err)
	}

	resps := []AgentResponse{{ID: agentID, RuntimeID: runtimeID, Status: "idle"}}
	testHandler.attachAgentRuntimeNames(context.Background(), resps)

	if resps[0].RuntimeDisplayStatus != agentDisplayStatusStarting {
		t.Fatalf("RuntimeDisplayStatus = %q, want %q — the agent-list endpoint must surface a fresh starting_since, not fall back to stale connectivity",
			resps[0].RuntimeDisplayStatus, agentDisplayStatusStarting)
	}
}
