package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestListAgentIssuePullRequests_RequiresAgentPrincipal(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/issues/"+uuid.NewString()+"/pull-requests", nil)
	h.ListAgentIssuePullRequests(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}

func TestListAgentIssuePullRequests_AgentPrincipalMissingIssueReturns404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Issue PR Missing", []byte("[]"))
	missingIssueID := uuid.NewString()
	req := newRequest(http.MethodGet, "/api/agent/issues/"+missingIssueID+"/pull-requests", nil)
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "id", missingIssueID)
	rec := httptest.NewRecorder()

	testHandler.ListAgentIssuePullRequests(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want exact 404", rec.Code, rec.Body.String())
	}
}
