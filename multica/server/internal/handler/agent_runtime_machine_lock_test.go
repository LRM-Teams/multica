package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestUpdateAgent_RejectsRuntimeChangeToDifferentMachine pins Frank's rule,
// 2026-08-02: an agent's bound computer cannot change — only which runtime on
// that same computer it uses. This is the authorization boundary the
// frontend's runtime picker already enforces cosmetically (runtime-picker.tsx
// filters to sameComputerRuntimes); this test proves a direct API call that
// bypasses the UI is rejected the same way, not silently allowed.
func TestUpdateAgent_RejectsRuntimeChangeToDifferentMachine(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	homeDaemonID := "machine-lock-home-" + uuid.NewString()
	homeRuntimeID := seedMachineLockedRuntime(t, homeDaemonID, "Machine Lock Home Runtime")
	otherDaemonID := "machine-lock-other-" + uuid.NewString()
	otherMachineRuntimeID := seedMachineLockedRuntime(t, otherDaemonID, "Machine Lock Other Runtime")

	agentID := createHandlerTestAgentOnRuntime(t, "machine-lock-reject-"+uuid.NewString()[:8], homeRuntimeID)

	rec := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"runtime_id": otherMachineRuntimeID,
	}), "id", agentID)
	testHandler.UpdateAgent(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-machine move status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error != "an agent's computer cannot be changed; choose a runtime on the same computer" {
		t.Fatalf("error message = %q, unexpected", body.Error)
	}

	var runtimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("read agent runtime after rejected move: %v", err)
	}
	if runtimeID != homeRuntimeID {
		t.Fatalf("agent runtime_id = %q after rejected move, want unchanged %q", runtimeID, homeRuntimeID)
	}
}

// TestUpdateAgent_AllowsRuntimeChangeOnSameMachine proves the rule is
// specifically "computer-locked," not "runtime-locked": switching between two
// runtimes that share a daemon_id (the same physical/virtual machine, e.g.
// moving from Claude to Codex on one installed daemon) must still succeed.
// Without this, TestUpdateAgent_RejectsRuntimeChangeToDifferentMachine alone
// could pass for the wrong reason — a handler that rejected every runtime_id
// change unconditionally would also satisfy it.
func TestUpdateAgent_AllowsRuntimeChangeOnSameMachine(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	daemonID := "machine-lock-same-" + uuid.NewString()
	firstRuntimeID := seedMachineLockedRuntime(t, daemonID, "Machine Lock Same-Machine First")
	secondRuntimeID := seedMachineLockedRuntime(t, daemonID, "Machine Lock Same-Machine Second")

	agentID := createHandlerTestAgentOnRuntime(t, "machine-lock-allow-"+uuid.NewString()[:8], firstRuntimeID)

	rec := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"runtime_id": secondRuntimeID,
	}), "id", agentID)
	testHandler.UpdateAgent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("same-machine move status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var runtimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("read agent runtime after same-machine move: %v", err)
	}
	if runtimeID != secondRuntimeID {
		t.Fatalf("agent runtime_id = %q after same-machine move, want %q", runtimeID, secondRuntimeID)
	}
}

// TestUpdateAgent_NoOpRuntimeUpdateNeverHitsMachineCheck proves resending the
// agent's current runtime_id (a no-op — every existing UpdateAgent caller
// that PATCHes unrelated fields alongside an unchanged runtime_id relies on
// this) doesn't require fetching/comparing a "current runtime" at all, so it
// can never spuriously 403 on a runtime whose own daemon_id happens to be
// NULL (its own unshareable machine by construction — see
// runtimesShareMachine).
func TestUpdateAgent_NoOpRuntimeUpdateNeverHitsMachineCheck(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "machine-lock-noop-"+uuid.NewString()[:8], nil)

	rec := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"runtime_id": testRuntimeID,
	}), "id", agentID)
	testHandler.UpdateAgent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("no-op runtime_id resend status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// TestRuntimesShareMachine pins runtimesShareMachine's exact semantics as a
// unit, independent of the HTTP handler: daemon_id is the only signal that
// counts, runtime_mode must also match, and a missing daemon_id on either
// side never matches anything (including another missing daemon_id — two
// daemon-less runtimes are two distinct machines, not the same "no machine").
func TestRuntimesShareMachine(t *testing.T) {
	withDaemon := func(mode, daemonID string) db.AgentRuntime {
		return db.AgentRuntime{RuntimeMode: mode, DaemonID: pgtype.Text{String: daemonID, Valid: true}}
	}
	noDaemon := func(mode string) db.AgentRuntime {
		return db.AgentRuntime{RuntimeMode: mode, DaemonID: pgtype.Text{Valid: false}}
	}

	cases := []struct {
		name string
		a, b db.AgentRuntime
		want bool
	}{
		{
			name: "same daemon_id, same runtime_mode",
			a:    withDaemon("local", "daemon-a"),
			b:    withDaemon("local", "daemon-a"),
			want: true,
		},
		{
			name: "different daemon_id",
			a:    withDaemon("local", "daemon-a"),
			b:    withDaemon("local", "daemon-b"),
			want: false,
		},
		{
			name: "same daemon_id but different runtime_mode",
			a:    withDaemon("local", "daemon-a"),
			b:    withDaemon("cloud", "daemon-a"),
			want: false,
		},
		{
			name: "both missing daemon_id never match, even each other",
			a:    noDaemon("cloud"),
			b:    noDaemon("cloud"),
			want: false,
		},
		{
			name: "one missing daemon_id never matches",
			a:    withDaemon("local", "daemon-a"),
			b:    noDaemon("local"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimesShareMachine(tc.a, tc.b); got != tc.want {
				t.Fatalf("runtimesShareMachine(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
