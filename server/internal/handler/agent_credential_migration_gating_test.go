package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedMigrationGatingReassignedAgent creates a second runtime (the "new"
// runtime an agent has been moved to) and an agent that used to live on
// handlerTestRuntimeID(t) (the "old" runtime), with runtime_reassigned_at
// stamped reassignedAt. Returns the agent id and old runtime id — the pair
// EnsureDaemonAgentCredential is called with to reproduce "a stale daemon on
// the old runtime asks about an agent that has since moved."
func seedMigrationGatingReassignedAgent(t *testing.T, reassignedAt time.Time) (agentID, oldRuntimeID string) {
	t.Helper()
	ctx := context.Background()

	oldRuntimeID = handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)

	var newRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'migration_gating_test', 'online', 'migration gating new runtime', now())
		RETURNING id`,
		testWorkspaceID, "migration-gating-new-"+uuid.NewString(), "Migration Gating New Runtime "+uuid.NewString(),
	).Scan(&newRuntimeID); err != nil {
		t.Fatalf("create new runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, newRuntimeID) })

	agentID = createHandlerTestAgent(t, "migration-gating-agent-"+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET runtime_id = $2, runtime_reassigned_at = $3 WHERE id = $1`,
		agentID, newRuntimeID, reassignedAt); err != nil {
		t.Fatalf("stamp agent runtime reassignment: %v", err)
	}

	return agentID, oldRuntimeID
}

func ensureDaemonAgentCredentialFor(t *testing.T, daemonID, runtimeID, agentID string) *httptest.ResponseRecorder {
	t.Helper()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential", nil, testWorkspaceID, daemonID)
	req = withRouteParams(req, "runtimeId", runtimeID, "agentId", agentID)
	rec := httptest.NewRecorder()
	testHandler.EnsureDaemonAgentCredential(rec, req)
	return rec
}

// TestUpdateAgent_MovingRuntimeStampsReassignmentMarker is the write side of
// task #38: UpdateAgent must stamp runtime_reassigned_at only when a request
// actually moves the agent to a different runtime, never on a no-op update
// that repeats the current runtime_id.
func TestUpdateAgent_MovingRuntimeStampsReassignmentMarker(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// task #(machine-lock, 2026-08-02): the agent's starting runtime and
	// otherRuntimeID must share a daemon_id — an agent's bound computer
	// cannot change, only the runtime within it — so both live on one
	// synthetic daemon here rather than starting from testRuntimeID (whose
	// daemon_id is NULL, making it its own unshareable machine).
	daemonID := "migration-gating-daemon-" + uuid.NewString()
	firstRuntimeID := seedMachineLockedRuntime(t, daemonID, "Migration Gating First Runtime")
	otherRuntimeID := seedMachineLockedRuntime(t, daemonID, "Migration Gating UpdateAgent Runtime")

	agentID := createHandlerTestAgentOnRuntime(t, "update-agent-stamp-"+uuid.NewString()[:8], firstRuntimeID)

	t.Run("no-op update on the same runtime does not stamp", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
			"runtime_id": firstRuntimeID,
		}), "id", agentID)
		testHandler.UpdateAgent(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("UpdateAgent (no-op): expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var stamped bool
		if err := testPool.QueryRow(ctx, `SELECT runtime_reassigned_at IS NOT NULL FROM agent WHERE id = $1`, agentID).Scan(&stamped); err != nil {
			t.Fatalf("load reassignment marker: %v", err)
		}
		if stamped {
			t.Fatal("runtime_reassigned_at should not be stamped by a no-op update repeating the current runtime_id")
		}
	})

	t.Run("moving to a different runtime stamps the marker", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
			"runtime_id": otherRuntimeID,
		}), "id", agentID)
		testHandler.UpdateAgent(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("UpdateAgent (move): expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var reassignedAt time.Time
		if err := testPool.QueryRow(ctx, `SELECT runtime_reassigned_at FROM agent WHERE id = $1`, agentID).Scan(&reassignedAt); err != nil {
			t.Fatalf("load reassignment marker: %v", err)
		}
		if time.Since(reassignedAt) > 10*time.Second {
			t.Fatalf("runtime_reassigned_at = %v, want a timestamp from just now", reassignedAt)
		}
	})
}

// TestEnsureDaemonAgentCredential_WithinGraceWindowReportsTransitionInProgress
// is the core of task #38: a stale daemon on the *old* runtime asking about
// an agent that moved seconds ago must get the new, distinct, retryable
// reason — not the #1628 terminal "agent is not bound to this runtime".
func TestEnsureDaemonAgentCredential_WithinGraceWindowReportsTransitionInProgress(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "agent-credential-daemon-" + uuid.NewString()
	agentID, oldRuntimeID := seedMigrationGatingReassignedAgent(t, time.Now().Add(-5*time.Second))
	seedHandlerTestRuntimeDaemonID(t, daemonID)

	rec := ensureDaemonAgentCredentialFor(t, daemonID, oldRuntimeID, agentID)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != runtimeTransitionInProgressReason {
		t.Fatalf("error = %q, want %q (must not be the #1628 terminal message)", body["error"], runtimeTransitionInProgressReason)
	}
}

// TestEnsureDaemonAgentCredential_OutsideGraceWindowReportsTerminalMismatch
// proves the grace window actually expires — the #1628 safety net (which
// #1628's daemon-side fix depends on for its terminal classification) must
// still fire once the window has passed, RED-GREEN against a naive "always
// silent if ever reassigned" implementation.
func TestEnsureDaemonAgentCredential_OutsideGraceWindowReportsTerminalMismatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "agent-credential-daemon-" + uuid.NewString()
	agentID, oldRuntimeID := seedMigrationGatingReassignedAgent(t, time.Now().Add(-agentRuntimeReassignmentGraceWindow-time.Second))
	seedHandlerTestRuntimeDaemonID(t, daemonID)

	rec := ensureDaemonAgentCredentialFor(t, daemonID, oldRuntimeID, agentID)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "agent is not bound to this runtime" {
		t.Fatalf("error = %q, want the #1628 terminal message once outside the grace window", body["error"])
	}
}

// TestEnsureDaemonAgentCredential_NoReassignmentMarkerReportsTerminalMismatch
// covers the common case predating #38 entirely: an agent bound to a
// different runtime with no runtime_reassigned_at on record (e.g. it was
// never touched by UpdateAgent's stamping path) always gets the terminal
// message immediately — no grace window applies.
func TestEnsureDaemonAgentCredential_NoReassignmentMarkerReportsTerminalMismatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "agent-credential-daemon-" + uuid.NewString()
	oldRuntimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)

	var newRuntimeID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'migration_gating_test', 'online', 'migration gating no-marker runtime', now())
		RETURNING id`,
		testWorkspaceID, "migration-gating-nomarker-"+uuid.NewString(), "Migration Gating No Marker Runtime "+uuid.NewString(),
	).Scan(&newRuntimeID); err != nil {
		t.Fatalf("create new runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, newRuntimeID) })

	agentID := createHandlerTestAgent(t, "migration-gating-nomarker-"+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, newRuntimeID); err != nil {
		t.Fatalf("bind agent to new runtime: %v", err)
	}

	rec := ensureDaemonAgentCredentialFor(t, daemonID, oldRuntimeID, agentID)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "agent is not bound to this runtime" {
		t.Fatalf("error = %q, want the #1628 terminal message when no reassignment marker exists", body["error"])
	}
}

// TestEnsureDaemonAgentCredential_SuccessOnNewRuntimeClearsReassignmentMarker
// is the "handoff confirmed" half: once the *new* runtime successfully
// ensures a credential, the marker must be cleared so a later, unrelated
// reassignment gets its own fresh grace window rather than inheriting this
// one's timestamp.
func TestEnsureDaemonAgentCredential_SuccessOnNewRuntimeClearsReassignmentMarker(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, _ := seedMigrationGatingReassignedAgent(t, time.Now().Add(-5*time.Second))

	var newRuntimeID string
	if err := testPool.QueryRow(context.Background(), `SELECT runtime_id FROM agent WHERE id = $1`, agentID).Scan(&newRuntimeID); err != nil {
		t.Fatalf("load agent's current runtime: %v", err)
	}
	newDaemonID := "agent-credential-daemon-new-" + uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET daemon_id = $1 WHERE id = $2`, newDaemonID, newRuntimeID); err != nil {
		t.Fatalf("bind new runtime's daemon id: %v", err)
	}
	// LRM-1570: ownership is machine-level; the new runtime resolves its
	// owner through an active binding for its daemon.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, 'migration-gating-success', TRUE)
	`, newDaemonID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("bind new runtime's owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1 AND workspace_id = $2`, newDaemonID, testWorkspaceID)
	})

	rec := ensureDaemonAgentCredentialFor(t, newDaemonID, newRuntimeID, agentID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (new runtime should ensure cleanly): %s", rec.Code, rec.Body.String())
	}

	var reassignedAtValid bool
	if err := testPool.QueryRow(context.Background(), `SELECT runtime_reassigned_at IS NOT NULL FROM agent WHERE id = $1`, agentID).Scan(&reassignedAtValid); err != nil {
		t.Fatalf("load reassignment marker: %v", err)
	}
	if reassignedAtValid {
		t.Fatal("runtime_reassigned_at should be cleared after a successful ensure on the new runtime")
	}
}
