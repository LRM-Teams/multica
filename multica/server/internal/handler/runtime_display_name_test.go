package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUpdateAgentRuntime_DisplayNamePatchAndClear covers PATCH display_name:
// set, clear (empty → fallback), length validation, and authz.
func TestUpdateAgentRuntime_DisplayNamePatchAndClear(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, runtimeOwnerID, plainMemberID := runtimeVisibilityFixture(t)

	// Owner sets a custom display_name.
	w := httptest.NewRecorder()
	req := newRequestAs(runtimeOwnerID, http.MethodPatch, "/api/runtimes/"+runtimeID, map[string]any{
		"display_name": "Studio Mac",
	})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH display_name: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentRuntimeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DisplayName != "Studio Mac" {
		t.Fatalf("display_name = %q, want Studio Mac", resp.DisplayName)
	}
	if resp.Name == "" {
		t.Fatalf("name should remain set (daemon/hostname fallback), got empty")
	}

	// Persist check.
	var stored string
	if err := testPool.QueryRow(context.Background(),
		`SELECT display_name FROM agent_runtime WHERE id = $1`, runtimeID,
	).Scan(&stored); err != nil {
		t.Fatalf("query display_name: %v", err)
	}
	if stored != "Studio Mac" {
		t.Fatalf("stored display_name = %q, want Studio Mac", stored)
	}

	// Empty string clears the override.
	w = httptest.NewRecorder()
	req = newRequestAs(runtimeOwnerID, http.MethodPatch, "/api/runtimes/"+runtimeID, map[string]any{
		"display_name": "  ",
	})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH clear display_name: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode clear: %v", err)
	}
	if resp.DisplayName != "" {
		t.Fatalf("cleared display_name = %q, want empty", resp.DisplayName)
	}

	// Too long → 400.
	w = httptest.NewRecorder()
	req = newRequestAs(runtimeOwnerID, http.MethodPatch, "/api/runtimes/"+runtimeID, map[string]any{
		"display_name": strings.Repeat("x", maxRuntimeDisplayNameLength+1),
	})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH oversized display_name: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Plain member cannot edit.
	w = httptest.NewRecorder()
	req = newRequestAs(plainMemberID, http.MethodPatch, "/api/runtimes/"+runtimeID, map[string]any{
		"display_name": "Hacked",
	})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("PATCH as plain member: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// TestDaemonRegister_PreservesUserDisplayName pins the AC that daemon
// register/upsert refreshes `name` but never clobbers a user-set display_name.
func TestDaemonRegister_PreservesUserDisplayName(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	daemonID := "display-name-preserve-daemon"
	provider := "claude"

	// First register as the workspace member so owner_id is set (daemon-token
	// register leaves owner NULL and the private runtime would be invisible
	// to ListVisibleAgentRuntimes).
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "host-a.local",
		"cli_version":  "v0.3.0",
		"runtimes": []map[string]any{
			{"name": "host-a.local", "type": provider, "version": "1.0.0", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first register: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var first map[string]any
	if err := json.NewDecoder(w.Body).Decode(&first); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	runtimes := first["runtimes"].([]any)
	rt := runtimes[0].(map[string]any)
	runtimeID := rt["id"].(string)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	if got, _ := rt["display_name"].(string); got != "" {
		t.Fatalf("fresh register display_name = %q, want empty", got)
	}
	if got, _ := rt["name"].(string); got != "host-a.local" {
		t.Fatalf("fresh register name = %q, want host-a.local", got)
	}

	// User renames via PATCH.
	w = httptest.NewRecorder()
	patch := newRequest(http.MethodPatch, "/api/runtimes/"+runtimeID, map[string]any{
		"display_name": "My Dev Box",
	})
	patch = withURLParam(patch, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, patch)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH display_name: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Re-register with a different hostname-derived name — display_name must stick.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "host-b.local",
		"cli_version":  "v0.3.1",
		"runtimes": []map[string]any{
			{"name": "host-b.local", "type": provider, "version": "1.0.1", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second register: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var second map[string]any
	if err := json.NewDecoder(w.Body).Decode(&second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	rt = second["runtimes"].([]any)[0].(map[string]any)
	if got, _ := rt["display_name"].(string); got != "My Dev Box" {
		t.Fatalf("after upsert display_name = %q, want My Dev Box (must not be overwritten)", got)
	}
	if got, _ := rt["name"].(string); got != "host-b.local" {
		t.Fatalf("after upsert name = %q, want host-b.local (daemon name should refresh)", got)
	}

	// List endpoint also surfaces both fields.
	w = httptest.NewRecorder()
	listReq := newRequest(http.MethodGet, "/api/runtimes", nil)
	testHandler.ListAgentRuntimes(w, listReq)
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgentRuntimes: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listed []AgentRuntimeResponse
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, item := range listed {
		if item.ID == runtimeID {
			found = true
			if item.DisplayName != "My Dev Box" {
				t.Fatalf("list display_name = %q, want My Dev Box", item.DisplayName)
			}
			if item.Name != "host-b.local" {
				t.Fatalf("list name = %q, want host-b.local", item.Name)
			}
			break
		}
	}
	if !found {
		t.Fatalf("runtime %s not found in list response", runtimeID)
	}

	// Daemon-token reconnect (production heartbeat/register path) must also
	// preserve display_name while refreshing name.
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "host-c.local",
		"cli_version":  "v0.3.2",
		"runtimes": []map[string]any{
			{"name": "host-c.local", "type": provider, "version": "1.0.2", "status": "online"},
		},
	}, testWorkspaceID, daemonID)
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("daemon-token re-register: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var third map[string]any
	if err := json.NewDecoder(w.Body).Decode(&third); err != nil {
		t.Fatalf("decode third: %v", err)
	}
	rt = third["runtimes"].([]any)[0].(map[string]any)
	if got, _ := rt["display_name"].(string); got != "My Dev Box" {
		t.Fatalf("daemon-token upsert display_name = %q, want My Dev Box", got)
	}
	if got, _ := rt["name"].(string); got != "host-c.local" {
		t.Fatalf("daemon-token upsert name = %q, want host-c.local", got)
	}
}
