package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateEnv_RequiresAuth(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/env", bytes.NewReader([]byte(`{}`)))
	h.CreateEnv(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestCreateEnv_RejectsMissingImageRef(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/env", bytes.NewReader([]byte(`{}`)))
	r.Header.Set("X-User-ID", "u1")
	r.Header.Set("X-Workspace-ID", "ws1")
	h.CreateEnv(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDeleteEnv_RequiresEnvID(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/v1/env/", nil)
	r.Header.Set("X-User-ID", "u1")
	r.Header.Set("X-Workspace-ID", "ws1")
	r.SetPathValue("envID", "")
	h.DeleteEnv(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
