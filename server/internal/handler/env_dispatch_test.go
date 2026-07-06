package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// doEnvDispatch builds an authenticated env-dispatch request with a workspace
// context and invokes h.EnvDispatch, mirroring the inline setup the other
// validation tests use. It relies on a DB-less handler (Queries == nil) so
// only the handler's UUID-shape gate + the service's validation gate are
// exercised; the stub deps short-circuit before any DB access.
func doEnvDispatch(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/env-dispatch", bytes.NewReader([]byte(body)))
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, r)
	return w
}

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

func TestEnvDispatch_RejectsBothAgentAndSquad(t *testing.T) {
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","squad_id":"` + validUUID + `","message":{"content":"hi"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("both agent+squad: want 400, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatch_AcceptsEmptyEnvIDShape(t *testing.T) {
	// empty env_id must not be rejected by the handler's UUID-shape gate
	// (which would emit "invalid env_id"); the service decides whether an
	// empty env_id is allowed (scratch self_play resolves a default).
	body := `{"mode":"scratch","env_id":"","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","message":{"content":"hi"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "invalid env_id") {
		t.Fatalf("empty env_id must pass the handler UUID gate, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatch_RejectsMalformedTrainAgentID(t *testing.T) {
	// A malformed train_agent_id must be rejected by the handler's UUID-shape
	// gate with a 400 (mirroring the agent_id/squad_id shape checks) instead of
	// panicking deeper in the adapter.
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","train_agent_id":"not-a-uuid","issue":{"title":"t"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (malformed train_agent_id must not panic)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid train_agent_id") {
		t.Fatalf("body = %s, want it to mention invalid train_agent_id", rr.Body.String())
	}
}

func TestEnvDispatch_AcceptsWellFormedTrainAgentID(t *testing.T) {
	// A well-formed train_agent_id equal to agent_id (single-agent training)
	// must pass the handler's UUID-shape gate. Using train_agent_id == agent_id
	// also satisfies the service validate() rule so no 400 is emitted.
	body := `{"mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"agent_id":"` + validUUID + `","train_agent_id":"` + validUUID + `","message":{"content":"hi"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "train_agent_id") {
		t.Fatalf("well-formed train_agent_id must pass shape validation, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatch_AcceptsResumeMode(t *testing.T) {
	body := `{"mode":"resume","env_id":"` + validUUID + `","domain":"swe_lego","dispatch_type":"issue","group_size":1,"agent_id":"` + validUUID + `","issue":{"title":"t"}}`
	rr := doEnvDispatch(t, body)
	if rr.Code == http.StatusBadRequest && strings.Contains(rr.Body.String(), "mode") {
		t.Fatalf("resume must be accepted as a mode, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEnvDispatchHandler_CriticAgentID_ShapeValidation(t *testing.T) {
	// 400 on malformed UUID
	body := `{"squad_id":"` + validUUID + `","mode":"scratch","env_id":"` + validUUID + `","domain":"self_play","dispatch_type":"message","group_size":1,"train_agent_id":"` + validUUID + `","critic_agent_id":"not-a-uuid","message":{"content":"hi"}}`
	req := httptest.NewRequest("POST", "/api/v1/env-dispatch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h := newTestHandler(Config{})
	w := httptest.NewRecorder()
	req.Header.Set("X-User-ID", "u1")
	req = req.WithContext(middleware.SetMemberContext(req.Context(), "ws1", db.Member{}))
	h.EnvDispatch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid critic_agent_id") {
		t.Fatalf("body = %s, want it to mention invalid critic_agent_id", w.Body.String())
	}
}
