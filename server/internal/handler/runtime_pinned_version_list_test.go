package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestListAgentRuntimes_SurfacesPinnedVersion is the GET /api/runtimes wiring
// check for task #81's pin status. Found by Wren while wiring the actual UI:
// the upgrade button lives on the machine/runtime detail page, which reads
// this list endpoint — not GET /agents, which was the only place the first
// pass of #81 (PR #1813) wired pinned_version into. GetAgentRuntime /
// ListAgentRuntimes / etc. are all normal sqlc `SELECT *` queries, but
// their checked-in generated code was hand-aligned (not regenerated, to keep
// task #85's drift out of this change) and had gone stale the same way
// attachAgentRuntimeNames did for #1801/#1802 — this test is the reason that
// won't recur silently.
func TestListAgentRuntimes_SurfacesPinnedVersion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "pinned-version-list-" + uuid.NewString()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/daemon/register", map[string]any{
		"workspace_id":   testWorkspaceID,
		"daemon_id":      daemonID,
		"device_name":    "pinned-list-test",
		"cli_version":    "0.3.85",
		"pinned_version": "0.3.85",
		"runtimes": []map[string]any{
			{"name": "pinned-list-test", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var registered map[string]any
	if err := json.NewDecoder(w.Body).Decode(&registered); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	runtimeID, _ := registered["runtimes"].([]any)[0].(map[string]any)["id"].(string)
	if runtimeID == "" {
		t.Fatal("register response missing runtime id")
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })

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
	var found *AgentRuntimeResponse
	for i := range listed {
		if listed[i].ID == runtimeID {
			found = &listed[i]
		}
	}
	if found == nil {
		t.Fatalf("runtime %s not found in ListAgentRuntimes response", runtimeID)
	}
	if found.PinnedVersion == nil || *found.PinnedVersion != "0.3.85" {
		t.Fatalf("PinnedVersion = %v, want \"0.3.85\" — GET /api/runtimes must surface agent_runtime.pinned_version", found.PinnedVersion)
	}
}
