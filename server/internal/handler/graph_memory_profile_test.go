package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Graph memory profile round-trip (design §1/A4): PUT persists the
// per-workspace reviewer settings, GET returns them, and a workspace
// without a profile row reads the legacy defaults.
func TestGraphMemoryProfileRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()

	// Dedicated workspace so the profile row cannot collide with other tests.
	var workspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, "Graph Memory Profile Test", "graph-memory-profile-test-"+uuid.NewString()[:8], "", "GMP").Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, workspaceID, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})

	// GET without a profile row returns the legacy defaults.
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/graph-memory/profile", nil), "id", workspaceID)
	req.Header.Set("X-Workspace-ID", workspaceID)
	testHandler.GetGraphMemoryProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetGraphMemoryProfile: status=%d body=%s", w.Code, w.Body.String())
	}
	var def graphMemoryProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&def); err != nil {
		t.Fatal(err)
	}
	if def.WorkspaceID != workspaceID || def.MemoryType != "legacy" || def.ExploreAgents != 4 || def.ExploreMaxRounds != 3 {
		t.Fatalf("default profile = %+v, want legacy/4/3", def)
	}

	// PUT persists the reviewer settings.
	w = httptest.NewRecorder()
	putReq := withURLParam(newRequest(http.MethodPut, "/api/workspaces/"+workspaceID+"/graph-memory/profile", map[string]any{
		"memory_type": "graph", "explore_agents": 2, "explore_max_rounds": 5,
	}), "id", workspaceID)
	putReq.Header.Set("X-Workspace-ID", workspaceID)
	testHandler.UpdateGraphMemoryProfile(w, putReq)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateGraphMemoryProfile: status=%d body=%s", w.Code, w.Body.String())
	}
	var saved graphMemoryProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.MemoryType != "graph" || saved.ExploreAgents != 2 || saved.ExploreMaxRounds != 5 || saved.UpdatedAt == "" {
		t.Fatalf("saved profile = %+v, want graph/2/5 with updated_at", saved)
	}

	// GET returns the persisted row.
	w = httptest.NewRecorder()
	getReq := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/graph-memory/profile", nil), "id", workspaceID)
	getReq.Header.Set("X-Workspace-ID", workspaceID)
	testHandler.GetGraphMemoryProfile(w, getReq)
	if w.Code != http.StatusOK {
		t.Fatalf("GetGraphMemoryProfile after PUT: status=%d body=%s", w.Code, w.Body.String())
	}
	var loaded graphMemoryProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&loaded); err != nil {
		t.Fatal(err)
	}
	if loaded != saved {
		t.Fatalf("loaded profile = %+v, want %+v", loaded, saved)
	}

	// Validation: an unknown reviewer type is rejected.
	w = httptest.NewRecorder()
	badReq := withURLParam(newRequest(http.MethodPut, "/api/workspaces/"+workspaceID+"/graph-memory/profile", map[string]any{
		"memory_type": "bogus", "explore_agents": 2, "explore_max_rounds": 5,
	}), "id", workspaceID)
	badReq.Header.Set("X-Workspace-ID", workspaceID)
	testHandler.UpdateGraphMemoryProfile(w, badReq)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateGraphMemoryProfile invalid type: status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}
