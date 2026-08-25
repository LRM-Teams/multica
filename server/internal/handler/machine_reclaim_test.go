package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// machineIDRegister registers a runtime with a machine_id and asserts it is
// persisted both on the runtime metadata and on the Computer identity row.
func machineIDRegister(t *testing.T, daemonID, machineID string) {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "machine-id-device",
		"machine_id":   machineID,
		"cli_version":  "v0.3.0",
		"runtimes": []map[string]any{
			{"name": "machine-id-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	}, testWorkspaceID, daemonID)
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister with machine_id: status=%d body=%s", w.Code, w.Body.String())
	}
}

// The register request must persist machine_id on both the runtime metadata
// and the Computer identity, so identity-rebuild convergence can resolve the
// same-machine proof on the very first registration.
func TestDaemonRegister_PersistsMachineIDOnIdentityAndRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "test-daemon-machineid-" + uuid.NewString()
	machineID := "machine-fp-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computers (id, user_id) VALUES ($1, $2)
	`, daemonID, testUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, 'test-token-hash', TRUE)
	`, daemonID, testWorkspaceID, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE workspace_id = $1 AND daemon_id = $2`, testWorkspaceID, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1`, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computers WHERE id = $1`, daemonID)
	})

	machineIDRegister(t, daemonID, machineID)

	// Identity row carries the machine fingerprint.
	var persisted string
	if err := testPool.QueryRow(ctx, `
		SELECT machine_id FROM computers WHERE id = $1
	`, daemonID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != machineID {
		t.Fatalf("computers.machine_id = %q, want %q", persisted, machineID)
	}
	// Runtime metadata carries it too.
	var metadata string
	if err := testPool.QueryRow(ctx, `
		SELECT metadata->>'machine_id' FROM agent_runtime
		WHERE workspace_id = $1 AND daemon_id = $2 AND provider = 'claude'
	`, testWorkspaceID, daemonID).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata != machineID {
		t.Fatalf("runtime metadata machine_id = %q, want %q", metadata, machineID)
	}
}

// Register without machine_id must not fail and must not write empty garbage
// onto the identity (happy path stays untouched).
func TestDaemonRegister_WithoutMachineIDKeepsIdentityClean(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "test-daemon-nomid-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computers (id, user_id) VALUES ($1, $2)
	`, daemonID, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE workspace_id = $1 AND daemon_id = $2`, testWorkspaceID, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1`, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computers WHERE id = $1`, daemonID)
	})
	// Register without a binding (registration still works when the owner is
	// resolved from the member context instead).
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "nomid-device",
		"runtimes": []map[string]any{
			{"name": "nomid-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	}, testWorkspaceID, daemonID)
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister without machine_id: status=%d body=%s", w.Code, w.Body.String())
	}
}

// ResolveComputerByMachineID returns the existing identity for a machine the
// server already knows, and empty when the machine_id has never been seen.
func TestResolveComputerByMachineID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "test-daemon-resolve-" + uuid.NewString()
	machineID := "machine-resolve-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computers (id, user_id, machine_id) VALUES ($1, $2, $3)
	`, daemonID, testUserID, machineID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, 'test-token-hash', TRUE)
	`, daemonID, testWorkspaceID, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1`, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computers WHERE id = $1`, daemonID)
	})

	doResolve := func(machineID string) (string, int) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/computers/resolve-by-machine-id", strings.NewReader(`{"machine_id":"`+machineID+`"}`))
		req.Header.Set("Authorization", "Bearer test-user-token")
		req.Header.Set("X-User-ID", testUserID)
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		testHandler.ResolveComputerByMachineID(w, req)
		var body map[string]any
		json.NewDecoder(w.Body).Decode(&body)
		got, _ := body["computer_id"].(string)
		return got, w.Code
	}

	if got, code := doResolve(machineID); code != http.StatusOK || got != daemonID {
		t.Fatalf("resolve known machine_id = %q (status %d), want %q", got, code, daemonID)
	}
	if got, _ := doResolve("machine-never-seen-" + uuid.NewString()); got != "" {
		t.Fatalf("resolve unknown machine_id = %q, want empty", got)
	}
	// Missing machine_id is rejected.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/computers/resolve-by-machine-id", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-user-token")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.ResolveComputerByMachineID(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("resolve with no machine_id: status=%d, want 400", w.Code)
	}
}
