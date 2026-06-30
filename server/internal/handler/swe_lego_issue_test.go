package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestCreateSweLegoIssue_RequiresAuth(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/swe-lego/issues", bytes.NewReader([]byte(`{}`)))
	h.CreateSweLegoIssue(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestCreateSweLegoIssue_RejectsMalformedBody(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/swe-lego/issues", bytes.NewReader([]byte(`not json`)))
	r.Header.Set("X-User-ID", "u1")
	h.CreateSweLegoIssue(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestCreateSweLegoIssue_RejectsMissingRequiredFields(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	// Valid JSON but missing repo_url / base_commit / issue_date.
	r := httptest.NewRequest("POST", "/api/v1/swe-lego/issues", bytes.NewReader([]byte(`{}`)))
	r.Header.Set("X-User-ID", "u1")
	h.CreateSweLegoIssue(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestCreateSweLegoIssue_RejectsInvalidGroupSize(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"repo_url":"r","base_commit":"c","issue_date":"d","group_size":0,"agent_config_id":"a"}`
	r := httptest.NewRequest("POST", "/api/v1/swe-lego/issues", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	h.CreateSweLegoIssue(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestCreateSweLegoIssue_Returns201WithStubIDsWhenFieldsValid(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"repo_url":"r","base_commit":"c","issue_date":"d","group_size":2,"agent_config_id":"a"}`
	r := httptest.NewRequest("POST", "/api/v1/swe-lego/issues", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	h.CreateSweLegoIssue(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	var resp CreateSweLegoIssueResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ProjectID != "stub-project" {
		t.Fatalf("expected stub-project, got %s", resp.ProjectID)
	}
	if len(resp.AgentRunIDs) != 2 {
		t.Fatalf("expected 2 agent run IDs, got %d", len(resp.AgentRunIDs))
	}
}

func TestDeleteSweLegoIssue_RequiresAuth(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/v1/swe-lego/issues/p1", nil)
	h.DeleteSweLegoIssue(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestDeleteSweLegoIssue_Returns204(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/v1/swe-lego/issues/p1", nil)
	r.Header.Set("X-User-ID", "u1")
	// Inject URL param via chi context (httptest doesn't run the router).
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("projectID", "p1")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chiCtx))
	h.DeleteSweLegoIssue(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}
