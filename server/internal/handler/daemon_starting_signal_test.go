package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestDaemonRegister_ClearsStartingSince proves a completed register is the
// authoritative "no longer starting" fact and must clear leftover
// starting_since from older daemons, not just wait for the read-side TTL.
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
