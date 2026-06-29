package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestCreateSweLegoIssue_Returns501WhenFieldsValid(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"repo_url":"r","base_commit":"c","issue_date":"d","group_size":2,"agent_config_id":"a"}`
	r := httptest.NewRequest("POST", "/api/v1/swe-lego/issues", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	h.CreateSweLegoIssue(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}
