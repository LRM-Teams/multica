package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseUniqueAgentIDsRejectsInvalidUUID(t *testing.T) {
	w := httptest.NewRecorder()
	if _, ok := parseUniqueAgentIDsOrBadRequest(w, []string{"not-a-uuid"}); ok {
		t.Fatal("parseUniqueAgentIDsOrBadRequest ok = true, want false")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAgentIDsBelongToWorkspaceRejectsForeignAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug)
		VALUES ('Memory Curation Foreign', 'memory-curation-foreign-' || substr(gen_random_uuid()::text, 1, 8))
		RETURNING id
	`).Scan(&otherWorkspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, device_info, owner_id)
		VALUES ($1, 'foreign memory runtime', 'local', 'legacy_local', 'foreign memory runtime', $2)
		RETURNING id
	`, otherWorkspaceID, testUserID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}

	var foreignAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id)
		VALUES ($1, 'foreign memory curation agent', 'local', $2, $3)
		RETURNING id
	`, otherWorkspaceID, runtimeID, testUserID).Scan(&foreignAgentID); err != nil {
		t.Fatal(err)
	}

	ok, err := testHandler.agentIDsBelongToWorkspace(ctx, testWorkspaceID, []string{foreignAgentID})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("agentIDsBelongToWorkspace ok = true, want false for foreign agent")
	}
}

func TestGetMemoryCurationRunRejectsInvalidRunID(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test database unavailable")
	}
	w := httptest.NewRecorder()
	req := withRouteParams(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/memory-curation/runs/not-a-uuid", nil), "id", testWorkspaceID, "runId", "not-a-uuid")
	testHandler.GetMemoryCurationRun(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGetAgentMemoryCurationStatusRejectsInvalidAgentID(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test database unavailable")
	}
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/agents/not-a-uuid/memory-curation/status", nil), "id", "not-a-uuid")
	testHandler.GetAgentMemoryCurationStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestPublicMemoryCurationStatsProjectsSharedPromotionCounts(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"agents_scanned":           3,
		"entries_promoted":         4,
		"shared_candidates_added":  2,
		"shared_candidates_synced": 1,
		"errors": []map[string]any{{
			"workspace_id": "workspace-1",
			"agent_id":     "agent-1",
			"stage":        "l3",
			"error":        "failed",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stats := publicMemoryCurationStats(raw)
	if stats.AgentsScanned != 3 || stats.EntriesPromoted != 4 {
		t.Fatalf("stats = %+v, want scanned=3 promoted=4", stats)
	}
	if stats.SharedCandidatesAdded != 2 || stats.SharedCandidatesSynced != 1 {
		t.Fatalf("stats = %+v, want shared added=2 synced=1", stats)
	}
	if stats.ErrorCount != 1 {
		t.Fatalf("error_count = %d, want 1", stats.ErrorCount)
	}
}

func TestPublicMemoryCurationStatsHandlesMalformedPayload(t *testing.T) {
	if got := publicMemoryCurationStats([]byte("not-json")); got != (memoryCurationRunStatsResponse{}) {
		t.Fatalf("stats = %+v, want zero value", got)
	}
}
