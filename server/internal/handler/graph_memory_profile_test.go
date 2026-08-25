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
	if def.WorkspaceID != workspaceID || def.MemoryType != "legacy" || def.GraphMemoryMode != "agent" ||
		def.ExploreAgents != 4 || def.ExploreMaxRounds != 6 || def.MemoryAgentIdleGraceSeconds != 120 ||
		def.MemoryAgentMaxNodesPerCall != 4 || def.MemoryAgentMaxNodesPerMinute != 30 ||
		def.MemoryAgentMaxContinuousTurnSeconds != 600 || def.MemoryAgentMaxTokensPerHour != 200000 {
		t.Fatalf("default profile = %+v, want legacy/agent defaults", def)
	}

	// PUT persists the reviewer settings.
	w = httptest.NewRecorder()
	putReq := withURLParam(newRequest(http.MethodPut, "/api/workspaces/"+workspaceID+"/graph-memory/profile", map[string]any{
		"memory_type": "graph", "graph_memory_mode": "inject", "explore_agents": 2, "explore_max_rounds": 5,
		"confirm_empty_start": true, "recall_ttt_enabled": true, "consolidation_ttt_enabled": false,
		"memory_agent_idle_grace_seconds": 180, "memory_agent_max_tokens_per_hour": 150000,
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
	if saved.MemoryType != "graph" || saved.GraphMemoryMode != "inject" || saved.ExploreAgents != 2 ||
		saved.ExploreMaxRounds != 5 || !saved.RecallTTTEnabled || saved.ConsolidationTTTEnabled ||
		saved.MemoryAgentIdleGraceSeconds != 180 || saved.MemoryAgentMaxTokensPerHour != 150000 || saved.UpdatedAt == "" {
		t.Fatalf("saved profile = %+v, want graph/inject with split TTT and agent limits", saved)
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

	w = httptest.NewRecorder()
	badModeReq := withURLParam(newRequest(http.MethodPut, "/api/workspaces/"+workspaceID+"/graph-memory/profile", map[string]any{
		"memory_type": "graph", "graph_memory_mode": "dual", "explore_agents": 2, "explore_max_rounds": 5,
		"config_version": saved.ConfigVersion,
	}), "id", workspaceID)
	badModeReq.Header.Set("X-Workspace-ID", workspaceID)
	testHandler.UpdateGraphMemoryProfile(w, badModeReq)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "graph_memory_mode") {
		t.Fatalf("UpdateGraphMemoryProfile invalid mode: status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}

// Effective profile values for the claimed-task path (spec §10): the helper
// returns the workspace's persisted memory type and explore knobs; a
// workspace without a profile row yields the zero value so callers fall back
// to the process env defaults.
func TestGraphMemoryProfileForWorkspaceReturnsExploreValues(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()

	var workspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, "Graph Memory Profile Values Test", "graph-memory-profile-values-"+uuid.NewString()[:8], "", "GMV").Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_profile (workspace_id, memory_type, explore_agents, explore_max_rounds)
		VALUES ($1, 'graph', 7, 9)`, workspaceID); err != nil {
		t.Fatal(err)
	}
	got := testHandler.graphMemoryProfileForWorkspace(ctx, parseUUID(workspaceID))
	if got.memoryType != "graph" || got.exploreAgents != 7 || got.exploreMaxRounds != 9 {
		t.Fatalf("profile values = %+v", got)
	}
	// No row -> zero value; callers then fall back to env defaults.
	got = testHandler.graphMemoryProfileForWorkspace(ctx, parseUUID("00000000-0000-0000-0000-000000000009"))
	if got != (graphMemoryProfileValues{}) {
		t.Fatalf("missing profile must yield zero values, got %+v", got)
	}
}

// Spec §11: switching a workspace TO graph memory requires explicit admin
// confirmation of the empty-start / no-fallback contract. Knob updates while
// already graph need no confirmation.
func TestUpdateGraphMemoryProfileSwitchToGraphRequiresConfirmation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	put := func(body string) *httptest.ResponseRecorder {
		req := withURLParam(newRequest(http.MethodPut,
			"/api/workspaces/"+workspaceID.String()+"/graph-memory/profile", json.RawMessage(body)), "id", workspaceID.String())
		rec := httptest.NewRecorder()
		testHandler.UpdateGraphMemoryProfile(rec, req)
		return rec
	}
	if rec := put(`{"memory_type":"graph","explore_agents":4,"explore_max_rounds":3}`); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "confirm_empty_start_required") {
		t.Fatalf("unconfirmed switch: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := put(`{"memory_type":"graph","explore_agents":4,"explore_max_rounds":3,"confirm_empty_start":true}`); rec.Code != http.StatusOK {
		t.Fatalf("confirmed switch: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Knob updates while already graph need no confirmation, but writes to an
	// existing row must carry the current config_version (spec §16 CAS).
	if rec := put(`{"memory_type":"graph","explore_agents":6,"explore_max_rounds":3}`); rec.Code != http.StatusConflict {
		t.Fatalf("unversioned knob update: status=%d, want 409", rec.Code)
	}
	if rec := put(`{"memory_type":"graph","explore_agents":6,"explore_max_rounds":3,"config_version":1}`); rec.Code != http.StatusOK {
		t.Fatalf("knob update: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
