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
