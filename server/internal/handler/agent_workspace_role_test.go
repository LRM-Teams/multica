package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// createHandlerTestMember inserts a new user and adds them to
// testWorkspaceID with the given role, returning the user id. Used to
// exercise UpdateAgentWorkspaceRole's actor-role authorization from a
// non-owner perspective.
//
// randomID() breaks both the name and email ties: "user" has a unique
// constraint on BOTH columns (user_name_unique + the email UNIQUE column),
// and without per-call entropy role+t.Name() alone collides with a leftover
// row from any prior interrupted run of the same test (task #86, same shape
// as task #78/#1807's insertSkillPromoteWorkspaceMember — that fix also had
// to randomize both columns for the same reason).
func createHandlerTestMember(t *testing.T, role string) string {
	t.Helper()
	suffix := randomID()
	var userID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Role Test "+role+" "+suffix, role+"-role-test-"+t.Name()+"-"+suffix+"@multica.ai").Scan(&userID); err != nil {
		t.Fatalf("create test member user: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
	`, testWorkspaceID, userID, role); err != nil {
		t.Fatalf("add test member (role=%s): %v", role, err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

// TestUpdateAgentWorkspaceRole_ActorAuthorization pins the task #32 fix:
// workspace admins (not just the owner) can edit an agent's workspace role.
// Asserts the specific status code, not just "non-200" — a 500 also
// satisfies "non-200" and would hide a crash behind a passing "denied" test.
func TestUpdateAgentWorkspaceRole_ActorAuthorization(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	tests := []struct {
		name       string
		actorRole  func(t *testing.T) string
		wantStatus int
	}{
		{"owner can edit", func(t *testing.T) string { return testUserID }, http.StatusOK},
		{"admin can edit", func(t *testing.T) string { return createHandlerTestMember(t, "admin") }, http.StatusOK},
		{"member cannot edit", func(t *testing.T) string { return createHandlerTestMember(t, "member") }, http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentID := createHandlerTestAgent(t, "Role Authz Test Agent", nil)
			actorID := tt.actorRole(t)

			req := withRouteParams(
				newRequest(http.MethodPatch, "/api/workspaces/"+testWorkspaceID+"/agents/"+agentID+"/role", map[string]string{
					"role": "admin",
				}),
				"id", testWorkspaceID,
				"agentId", agentID,
			)
			req.Header.Set("X-User-ID", actorID)

			w := httptest.NewRecorder()
			testHandler.UpdateAgentWorkspaceRole(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.wantStatus, w.Body.String())
			}

			var role string
			if err := testPool.QueryRow(context.Background(), `
				SELECT workspace_role FROM agent WHERE id = $1
			`, agentID).Scan(&role); err != nil {
				t.Fatalf("read agent workspace role: %v", err)
			}
			if tt.wantStatus == http.StatusOK {
				if role != "admin" {
					t.Fatalf("workspace_role = %q, want admin (edit should have applied)", role)
				}
			} else {
				if role == "admin" {
					t.Fatalf("workspace_role = %q, want unchanged from default (edit should have been rejected)", role)
				}
			}
		})
	}
}

// TestUpdateAgentWorkspaceRole_AdminCannotSetOwner pins that widening actor
// authorization to owner+admin (task #32) did not open a path to granting
// an agent the "owner" role. The target-role whitelist (member/admin only)
// runs before the actor-role check and is unaffected by it — an admin who
// posts role=owner must get a clean 400, never 200 or 500.
func TestUpdateAgentWorkspaceRole_AdminCannotSetOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Role Owner Escalation Test Agent", nil)
	adminID := createHandlerTestMember(t, "admin")

	req := withRouteParams(
		newRequest(http.MethodPatch, "/api/workspaces/"+testWorkspaceID+"/agents/"+agentID+"/role", map[string]string{
			"role": "owner",
		}),
		"id", testWorkspaceID,
		"agentId", agentID,
	)
	req.Header.Set("X-User-ID", adminID)

	w := httptest.NewRecorder()
	testHandler.UpdateAgentWorkspaceRole(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (role=owner must be rejected as an invalid target role): %s", w.Code, w.Body.String())
	}

	var role string
	if err := testPool.QueryRow(context.Background(), `
		SELECT workspace_role FROM agent WHERE id = $1
	`, agentID).Scan(&role); err != nil {
		t.Fatalf("read agent workspace role: %v", err)
	}
	if role == "owner" {
		t.Fatalf("workspace_role = %q, want unchanged (owner escalation must not apply)", role)
	}
}

// TestUpdateAgentWorkspaceRolePublishesCanonicalBroadcast reflects the
// 2026-07-31 Wendy DM incident fix: a Wendy-named agent's workspace-role
// change now broadcasts workspace-wide like any other agent — no owner-only
// recipient scoping.
func TestUpdateAgentWorkspaceRolePublishesCanonicalBroadcast(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Wendy", nil)
	roleBus := events.New()
	oldBus := testHandler.Bus
	testHandler.Bus = roleBus
	t.Cleanup(func() {
		testHandler.Bus = oldBus
	})

	var got []events.Event
	roleBus.Subscribe(protocol.EventAgentStatus, func(event events.Event) {
		got = append(got, event)
	})

	req := withRouteParams(
		newRequest(http.MethodPatch, "/api/workspaces/"+testWorkspaceID+"/agents/"+agentID+"/role", map[string]string{
			"role": "admin",
		}),
		"id", testWorkspaceID,
		"agentId", agentID,
	)
	w := httptest.NewRecorder()
	testHandler.UpdateAgentWorkspaceRole(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update workspace role status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var role string
	if err := testPool.QueryRow(context.Background(), `
		SELECT workspace_role
		FROM agent
		WHERE id = $1
	`, agentID).Scan(&role); err != nil {
		t.Fatalf("read updated workspace role: %v", err)
	}
	if role != "admin" {
		t.Fatalf("workspace role = %q, want admin", role)
	}

	if len(got) != 1 {
		t.Fatalf("role-change events = %d, want 1", len(got))
	}
	if len(got[0].RecipientUserIDs) != 0 {
		t.Fatalf("role-change recipients = %#v, want workspace-wide broadcast (no owner-only scoping)", got[0].RecipientUserIDs)
	}
	payloadJSON, err := json.Marshal(got[0].Payload)
	if err != nil {
		t.Fatalf("marshal role-change event payload: %v", err)
	}
	var payload struct {
		Agent struct {
			ID            string `json:"id"`
			WorkspaceRole string `json:"workspace_role"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatalf("decode role-change event payload: %v", err)
	}
	if payload.Agent.ID != agentID || payload.Agent.WorkspaceRole != "admin" {
		t.Fatalf("role-change event agent = %#v, want id=%s workspace_role=admin", payload.Agent, agentID)
	}

	getW := httptest.NewRecorder()
	testHandler.GetAgent(getW, withURLParam(newRequest(http.MethodGet, "/api/agents/"+agentID, nil), "id", agentID))
	if getW.Code != http.StatusOK {
		t.Fatalf("get agent status = %d, want 200: %s", getW.Code, getW.Body.String())
	}
	var gotAgent AgentResponse
	if err := json.Unmarshal(getW.Body.Bytes(), &gotAgent); err != nil {
		t.Fatalf("decode agent response: %v", err)
	}
	if gotAgent.ID != agentID || gotAgent.WorkspaceRole != "admin" {
		t.Fatalf("agent response = id:%s workspace_role:%s, want id:%s workspace_role:admin",
			gotAgent.ID, gotAgent.WorkspaceRole, agentID)
	}

	noopReq := withRouteParams(
		newRequest(http.MethodPatch, "/api/workspaces/"+testWorkspaceID+"/agents/"+agentID+"/role", map[string]string{
			"role": "admin",
		}),
		"id", testWorkspaceID,
		"agentId", agentID,
	)
	noopW := httptest.NewRecorder()
	testHandler.UpdateAgentWorkspaceRole(noopW, noopReq)
	if noopW.Code != http.StatusOK {
		t.Fatalf("idempotent workspace role status = %d, want 200: %s", noopW.Code, noopW.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("idempotent role change published event: got %d, want 1", len(got))
	}
}
