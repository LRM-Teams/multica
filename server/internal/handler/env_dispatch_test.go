package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestEnvDispatch_RequiresAuth(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(`{}`)))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// validUUID is a syntactically valid UUID used by handler tests that need to
// pass the handler's UUID-shape gate and exercise deeper (service) validation.
const validUUID = "11111111-1111-1111-1111-111111111111"

func TestEnvDispatch_RejectsMissingMode(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"env_id":"` + validUUID + `","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","domain":"swe_lego","issue":{"title":"t"}}`
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestEnvDispatch_RejectsMalformedEnvID(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"mode":"scratch","env_id":"not-a-uuid","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (malformed env_id must not panic)", w.Code)
	}
}

func TestEnvDispatch_RejectsSweLegoMessage(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"q"}}`
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestEnvDispatch_SelfPlayIssue_Returns501(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}

func TestDeleteEnvDispatchProject_RequiresProjectID(t *testing.T) {
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/v1/env-dispatch/", nil)
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.DeleteEnvDispatchProject(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
