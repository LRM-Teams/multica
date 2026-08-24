package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// mustGraphMemoryMember grants the shared test user a role on the workspace
// so member/role-gated graph memory handlers resolve membership.
func mustGraphMemoryMember(t *testing.T, workspaceID pgtype.UUID, role string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)
	`, workspaceID, testUserID, role); err != nil {
		t.Fatal(err)
	}
}

func TestStartGraphMemoryConsolidationRequiresReadyGraphWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	// graph profile WITHOUT scoped_writer_ready -> 409 graph_not_ready.
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO graph_memory_profile (workspace_id, memory_type) VALUES ($1, 'graph')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	req := withURLParam(newRequest(http.MethodPost,
		"/api/workspaces/"+workspaceID.String()+"/graph-memory/consolidations", nil), "id", workspaceID.String())
	rec := httptest.NewRecorder()
	testHandler.StartGraphMemoryConsolidation(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "graph_not_ready") {
		t.Fatalf("not-ready workspace: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// legacy workspace -> same stable refusal.
	legacyWS := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, legacyWS, "owner")
	req = withURLParam(newRequest(http.MethodPost,
		"/api/workspaces/"+legacyWS.String()+"/graph-memory/consolidations", nil), "id", legacyWS.String())
	rec = httptest.NewRecorder()
	testHandler.StartGraphMemoryConsolidation(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("legacy workspace: status=%d, want 409", rec.Code)
	}
}
