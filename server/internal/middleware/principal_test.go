package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentPrincipalRoundTrip(t *testing.T) {
	p := AgentPrincipal{
		AgentID:     "11111111-1111-1111-1111-111111111111",
		WorkspaceID: "22222222-2222-2222-2222-222222222222",
		OwnerUserID: "33333333-3333-3333-3333-333333333333",
		ActorSource: "agent_credential",
	}
	ctx := WithAgentPrincipal(context.Background(), p)
	got, ok := AgentPrincipalFromContext(ctx)
	if !ok {
		t.Fatal("expected agent principal")
	}
	if got.AgentID != p.AgentID || got.WorkspaceID != p.WorkspaceID || got.ActorSource != p.ActorSource {
		t.Fatalf("got %+v want %+v", got, p)
	}
	if _, ok := HumanPrincipalFromContext(ctx); ok {
		t.Fatal("did not expect human principal")
	}
}

func TestRequireAgentPrincipalRejectsHuman(t *testing.T) {
	h := RequireAgentPrincipal(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/channels", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/agent/channels", nil)
	ctx := WithAgentPrincipal(req.Context(), AgentPrincipal{
		AgentID: "11111111-1111-1111-1111-111111111111", WorkspaceID: "22222222-2222-2222-2222-222222222222", ActorSource: "agent_credential",
	})
	h.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", rec.Code)
	}
}
