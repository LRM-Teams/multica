package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCanUpdateAgent_AgentPrincipalSelfOnly(t *testing.T) {
	h := &Handler{}
	selfID := "11111111-1111-1111-1111-111111111111"
	otherID := "22222222-2222-2222-2222-222222222222"
	var selfUUID, otherUUID pgtype.UUID
	_ = selfUUID.Scan(selfID)
	_ = otherUUID.Scan(otherID)

	selfAgent := db.Agent{ID: selfUUID}
	otherAgent := db.Agent{ID: otherUUID}

	withPrincipal := func(agentID string) *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/api/agents/"+agentID, nil)
		ctx := middleware.WithAgentPrincipal(req.Context(), middleware.AgentPrincipal{
			AgentID:     selfID,
			WorkspaceID: "33333333-3333-3333-3333-333333333333",
			OwnerUserID: "44444444-4444-4444-4444-444444444444",
			ActorSource: "agent_credential",
		})
		return req.WithContext(ctx)
	}

	// self: allowed
	rec := httptest.NewRecorder()
	if !h.canUpdateAgent(rec, withPrincipal(selfID), selfAgent, nil) {
		t.Fatalf("self update denied: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// other: 403
	rec = httptest.NewRecorder()
	if h.canUpdateAgent(rec, withPrincipal(otherID), otherAgent, nil) {
		t.Fatal("other-agent update must be denied")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
}

func TestCanManageAgent_AgentPrincipalSelfOnly(t *testing.T) {
	h := &Handler{}
	selfID := "11111111-1111-1111-1111-111111111111"
	otherID := "22222222-2222-2222-2222-222222222222"
	var selfUUID, otherUUID pgtype.UUID
	_ = selfUUID.Scan(selfID)
	_ = otherUUID.Scan(otherID)

	withPrincipal := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/agents/"+otherID+"/archive", nil)
		ctx := middleware.WithAgentPrincipal(req.Context(), middleware.AgentPrincipal{
			AgentID:     selfID,
			WorkspaceID: "33333333-3333-3333-3333-333333333333",
			OwnerUserID: "44444444-4444-4444-4444-444444444444",
			ActorSource: "agent_credential",
		})
		return req.WithContext(ctx)
	}

	rec := httptest.NewRecorder()
	if h.canManageAgent(rec, withPrincipal(), db.Agent{ID: otherUUID}) {
		t.Fatal("archive other agent must be denied")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	if !h.canManageAgent(rec, withPrincipal(), db.Agent{ID: selfUUID}) {
		t.Fatalf("self archive must be allowed at canManage gate: %s", rec.Body.String())
	}
}
