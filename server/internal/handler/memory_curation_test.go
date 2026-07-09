package handler

import (
	"context"
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

	var foreignAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, owner_id)
		VALUES ($1, 'foreign memory curation agent', 'local', $2)
		RETURNING id
	`, otherWorkspaceID, testUserID).Scan(&foreignAgentID); err != nil {
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
