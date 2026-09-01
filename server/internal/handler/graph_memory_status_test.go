package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// Slice 3.3 (unification spec §4.4/§10): the governance status endpoint
// reports the federated research graph — version, staging depth (always 0:
// research imports never stage), and node count — and reports nothing for
// legacy workspaces.
func TestGraphMemoryStatusResearchGraph(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO graph_memory_profile (workspace_id, memory_type, scoped_writer_ready) VALUES ($1, 'graph', true)`, workspaceID); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	dir, err := memorygraph.EnsureScopedDir(root, workspaceID.String(), memorygraph.GraphDirKindResearch, workspaceID.String())
	if err != nil {
		t.Fatal(err)
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, node := range []string{"insight-1", "result-1"} {
		if err := store.SaveNode(1, &memorygraph.Node{
			NodeID: node, Body: node + " body", Visibility: "research",
			CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1,
			ObservedAt: now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+workspaceID.String()+"/graph-memory/status", nil), "id", workspaceID.String())
	testHandler.GetGraphMemoryStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		MemoryType string `json:"memory_type"`
		Graphs     []struct {
			Kind            string `json:"kind"`
			OwnerID         string `json:"owner_id"`
			CurrentVersion  int    `json:"current_version"`
			StagingSegments int    `json:"staging_segments"`
			NodeCount       int    `json:"node_count"`
		} `json:"graphs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MemoryType != "graph" {
		t.Fatalf("memory_type=%q, want graph", payload.MemoryType)
	}
	if len(payload.Graphs) != 1 {
		t.Fatalf("graphs=%+v, want exactly the research graph", payload.Graphs)
	}
	g := payload.Graphs[0]
	if g.Kind != "research" || g.OwnerID != workspaceID.String() {
		t.Fatalf("research graph = %+v, want kind research owned by the workspace", g)
	}
	if g.CurrentVersion != 1 || g.StagingSegments != 0 {
		t.Fatalf("research graph stats = %+v, want version 1 and staging 0", g)
	}
	if g.NodeCount != 2 {
		t.Fatalf("research node_count=%d, want 2", g.NodeCount)
	}

	// Legacy workspace: no graph dirs, no research row.
	legacyWS := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, legacyWS, "owner")
	rec = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+legacyWS.String()+"/graph-memory/status", nil), "id", legacyWS.String())
	testHandler.GetGraphMemoryStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MemoryType != "legacy" || len(payload.Graphs) != 0 {
		t.Fatalf("legacy status = memory_type %q graphs %+v, want legacy with no graphs", payload.MemoryType, payload.Graphs)
	}
}
