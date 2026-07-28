package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestListAgentIssueAttachments_RequiresAgentPrincipal(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/issues/"+uuid.NewString()+"/attachments", nil)
	h.ListAgentIssueAttachments(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}

func TestListAgentChannelAttachments_RequiresAgentPrincipal(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/channels/"+uuid.NewString()+"/attachments", nil)
	h.ListAgentChannelAttachments(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}
