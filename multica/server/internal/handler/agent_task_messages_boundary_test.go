package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestListAgentTaskMessages_RequiresAgentPrincipal(t *testing.T) {
	h := &Handler{}
	taskID := uuid.NewString()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/tasks/"+taskID+"/messages", nil)
	req = withURLParam(req, "taskId", taskID)

	h.ListAgentTaskMessages(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want exact 403", rec.Code, rec.Body.String())
	}
}

func TestListAgentTaskMessages_AgentPrincipalMissingTaskReturns404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Task Messages Missing", []byte("[]"))
	missingTaskID := uuid.NewString()
	req := newRequest(http.MethodGet, "/api/agent/tasks/"+missingTaskID+"/messages", nil)
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "taskId", missingTaskID)
	rec := httptest.NewRecorder()

	testHandler.ListAgentTaskMessages(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want exact 404", rec.Code, rec.Body.String())
	}
}

func TestListAgentTaskMessages_AgentPrincipalWorkspaceTaskReturns200(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Task Messages Read", []byte("[]"))
	taskID := createHandlerTestTaskForAgent(t, agentID)
	req := newRequest(http.MethodGet, "/api/agent/tasks/"+taskID+"/messages", nil)
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "taskId", taskID)
	rec := httptest.NewRecorder()

	testHandler.ListAgentTaskMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
}
