package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Spec §16: workspace profile updates use a config-version compare-and-set
// contract; stale or unversioned full-profile writes conflict instead of
// silently overwriting concurrent changes. The response carries every
// Dive-era tunable (spec §2).
func TestUpdateGraphMemoryProfileCASContract(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	ws := workspaceID.String()

	put := func(body map[string]any) *httptest.ResponseRecorder {
		req := withURLParam(newRequest(http.MethodPut,
			"/api/workspaces/"+ws+"/graph-memory/profile", body), "id", ws)
		req.Header.Set("X-Workspace-ID", ws)
		rec := httptest.NewRecorder()
		testHandler.UpdateGraphMemoryProfile(rec, req)
		return rec
	}
	type profileResp struct {
		ConfigVersion            int64   `json:"config_version"`
		TTTEnabled               bool    `json:"ttt_enabled"`
		ExploreAgents            int32   `json:"explore_agents"`
		ExploreNodesPerExpansion int32   `json:"explore_nodes_per_expansion"`
		MaxHierarchyFanout       int32   `json:"max_hierarchy_fanout"`
		DiveMaxRounds            int32   `json:"dive_max_rounds"`
		DiveTimeoutSeconds       int32   `json:"dive_timeout_seconds"`
		WRound                   float64 `json:"w_round"`
		DiveModel                string  `json:"dive_model"`
		DiveProvider             string  `json:"dive_provider"`
	}
	decode := func(rec *httptest.ResponseRecorder) profileResp {
		t.Helper()
		var p profileResp
		if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Create (no existing row) needs no version.
	rec := put(map[string]any{
		"memory_type": "graph", "explore_agents": 4, "explore_max_rounds": 3, "confirm_empty_start": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}
	created := decode(rec)
	if created.ConfigVersion != 1 || created.TTTEnabled {
		t.Fatalf("created profile = %+v, want config_version=1 ttt disabled", created)
	}

	// Versioned update persists the Dive tunables and bumps the version.
	rec = put(map[string]any{
		"memory_type": "graph", "explore_agents": 4, "explore_max_rounds": 3,
		"config_version": 1, "ttt_enabled": true, "explore_nodes_per_expansion": 2,
		"max_hierarchy_fanout": 6, "dive_max_rounds": 8, "dive_timeout_seconds": 300, "w_round": 0.2,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", rec.Code, rec.Body.String())
	}
	updated := decode(rec)
	if updated.ConfigVersion != 2 || !updated.TTTEnabled || updated.ExploreNodesPerExpansion != 2 ||
		updated.MaxHierarchyFanout != 6 || updated.DiveMaxRounds != 8 || updated.DiveTimeoutSeconds != 300 ||
		updated.WRound != 0.2 {
		t.Fatalf("updated profile = %+v", updated)
	}

	// GET returns the persisted tunables.
	getReq := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+ws+"/graph-memory/profile", nil), "id", ws)
	getReq.Header.Set("X-Workspace-ID", ws)
	getRec := httptest.NewRecorder()
	testHandler.GetGraphMemoryProfile(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get: status=%d", getRec.Code)
	}
	if got := decode(getRec); !got.TTTEnabled || got.ExploreNodesPerExpansion != 2 || got.ConfigVersion != 2 {
		t.Fatalf("loaded profile = %+v", got)
	}

	// Stale and unversioned writes against an existing row conflict.
	if rec := put(map[string]any{
		"memory_type": "graph", "explore_agents": 4, "explore_max_rounds": 3, "config_version": 1,
	}); rec.Code != http.StatusConflict {
		t.Fatalf("stale write: status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if rec := put(map[string]any{
		"memory_type": "graph", "explore_agents": 4, "explore_max_rounds": 3,
	}); rec.Code != http.StatusConflict {
		t.Fatalf("unversioned write: status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}

	// Over-ceiling values and out-of-policy Dive overrides fail closed.
	if rec := put(map[string]any{
		"memory_type": "graph", "explore_agents": 4, "explore_max_rounds": 3,
		"config_version": 2, "max_hierarchy_fanout": 65,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("over-ceiling write: status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if rec := put(map[string]any{
		"memory_type": "graph", "explore_agents": 4, "explore_max_rounds": 3,
		"config_version": 2, "dive_provider": "ark", "dive_model": "glm-5.3",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("dive override without allow-list: status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}

	// Every rejected write left the row at version 2.
	var version int64
	if err := testPool.QueryRow(ctx,
		`SELECT config_version FROM graph_memory_profile WHERE workspace_id = $1`, ws).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("config_version = %d after rejected writes, want 2", version)
	}
}
