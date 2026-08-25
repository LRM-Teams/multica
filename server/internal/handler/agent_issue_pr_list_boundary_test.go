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

func TestRescanAgentIssuePullRequest_RequiresAgentPrincipal(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/agent/issues/"+uuid.NewString()+"/pull-requests/rescan", nil)
	h.RescanAgentIssuePullRequest(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}

func TestRescanAgentIssuePullRequest_CrossWorkspaceIssueReturns404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	repo := "rescan-cross-workspace-" + randomID()
	projectID := createGitHubRescanProject(t, repo)
	issue := createGitHubRescanIssue(t, projectID, "Cross-workspace rescan")
	agentID := createHandlerTestAgent(t, "Issue PR Cross Workspace", []byte("[]"))
	req := newRequest(http.MethodPost, "/api/agent/issues/"+issue.ID+"/pull-requests/rescan", map[string]any{
		"pull_request_number": 73,
	})
	req = withAgentPrincipal(req, agentID, uuid.NewString(), testUserID)
	req = withURLParam(req, "id", issue.ID)
	rec := httptest.NewRecorder()

	testHandler.RescanAgentIssuePullRequest(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want exact 404", rec.Code, rec.Body.String())
	}
}
