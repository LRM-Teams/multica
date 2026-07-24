package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListMemoryCurationDailySummaryRequiresWorkspaceID(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces//memory-curation/daily-summary", nil)
	w := httptest.NewRecorder()
	h.ListMemoryCurationDailySummary(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestListMemoryCurationCandidatesRequiresDate(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test database unavailable")
	}
	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/memory-curation/candidates", nil), "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListMemoryCurationCandidates(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
