package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestOnboardingAgentLifecycle_IsOwnerOnlyAndPreservesBinding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "lifecycle_onboarding_"+strings.ReplaceAll(uuid.NewString(), "-", "_"), nil)
	bindOnboardingAgentForTest(t, agentID)
	adminSuffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	adminID := createWorkspaceMemberUser(t, "Lifecycle Admin "+adminSuffix, "lifecycle-admin-"+adminSuffix+"@multica.test")
	if _, err := testPool.Exec(ctx, `UPDATE member SET role = 'admin' WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, adminID); err != nil {
		t.Fatal(err)
	}

	archive := func(userID string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := withURLParam(newRequestAs(userID, http.MethodDelete, "/api/agents/"+agentID, nil), "id", agentID)
		testHandler.ArchiveAgent(rec, req)
		return rec
	}
	restore := func(userID string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := withURLParam(newRequestAs(userID, http.MethodPost, "/api/agents/"+agentID+"/restore", nil), "id", agentID)
		testHandler.RestoreAgent(rec, req)
		return rec
	}

	if rec := archive(adminID); rec.Code != http.StatusForbidden {
		t.Fatalf("admin archive=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := archive(testUserID); rec.Code != http.StatusOK {
		t.Fatalf("owner archive=%d body=%s", rec.Code, rec.Body.String())
	}
	var boundID string
	if err := testPool.QueryRow(ctx, `SELECT onboarding_agent_id FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&boundID); err != nil || boundID != agentID {
		t.Fatalf("binding after archive=%q err=%v, want %s", boundID, err, agentID)
	}
	if testHandler.isActiveOnboardingAgent(ctx, parseUUID(testWorkspaceID), parseUUID(agentID)) {
		t.Fatal("archived Onboarding Agent retained hiring capability")
	}
	if rec := restore(adminID); rec.Code != http.StatusForbidden {
		t.Fatalf("admin restore=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := restore(testUserID); rec.Code != http.StatusOK {
		t.Fatalf("owner restore=%d body=%s", rec.Code, rec.Body.String())
	}
	if !testHandler.isActiveOnboardingAgent(ctx, parseUUID(testWorkspaceID), parseUUID(agentID)) {
		t.Fatal("restored Onboarding Agent did not regain hiring capability")
	}
}
