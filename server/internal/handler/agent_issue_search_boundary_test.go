package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
)

// #1319 successor (Barry post-merge): handler-level boundary for issue search.
// CLI path contracts already covered in cmd/multica; these pin the server gate.

func TestSearchAgentIssues_RequiresAgentPrincipal(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/issues/search?q=login", nil)
	h.SearchAgentIssues(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no AgentPrincipal: status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
}

func TestSearchAgentIssues_AgentPrincipal_MissingQueryIsBadRequest(t *testing.T) {
	// Passing principal gate then failing on missing q proves SearchAgentIssues
	// reaches shared SearchIssues (not stuck at principal).
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/issues/search", nil)
	agentID := uuid.NewString()
	wsID := uuid.NewString()
	ownerID := uuid.NewString()
	req = withAgentPrincipal(req, agentID, wsID, ownerID)
	req.Header.Set("X-Workspace-ID", wsID)
	h.SearchAgentIssues(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("agent principal missing q: status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
}

func TestSearchAgentIssues_WorkspaceBound_IgnoresForgedWorkspaceQuery(t *testing.T) {
	// Machine credential path: ResolveWorkspaceIDFromRequest must prefer
	// server-bound X-Workspace-ID over client-forged workspace_id query.
	// SearchIssues uses the same resolver via resolveWorkspaceID.
	bound := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	forged := "ffffffff-0000-0000-0000-ffffffffffff"
	req := httptest.NewRequest(http.MethodGet, "/api/agent/issues/search?q=x&workspace_id="+forged, nil)
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Workspace-ID", bound)
	got := middleware.ResolveWorkspaceIDFromRequest(req, nil)
	if got != bound {
		t.Fatalf("workspace = %q, want bound %q (forged query must not widen)", got, bound)
	}
}
