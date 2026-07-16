package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// markGroupManagerForTest flags an agent as a per-group Beckham and makes it
// workspace-visible (mirrors how Beckham is actually provisioned).
func markGroupManagerForTest(t *testing.T, agentID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET managed_role = 'group_manager', visibility = 'workspace' WHERE id = $1`, agentID,
	); err != nil {
		t.Fatalf("mark group manager: %v", err)
	}
}

// A group-manager agent (Beckham) is shared infrastructure: any workspace
// member can tune its runtime config, and the API stamps managed_role so the
// UI can surface the config tab.
func TestUpdateAgent_GroupManagerEditableByPlainMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "beckham-config-"+uuid.NewString(), nil)
	markGroupManagerForTest(t, agentID)
	memberID := createWorkspaceMemberUser(t, "cfg-member", "cfg-member-"+uuid.NewString()+"@example.com")

	req := newRequestAs(memberID, http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"max_concurrent_tasks": 3,
	})
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("plain member editing group manager: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ManagedRole != "group_manager" {
		t.Errorf("managed_role = %q, want group_manager", resp.ManagedRole)
	}
	if resp.MaxConcurrentTasks != 3 {
		t.Errorf("max_concurrent_tasks = %d, want 3 (edit not applied)", resp.MaxConcurrentTasks)
	}
}

// A normal (non-group-manager) agent stays owner/admin-gated: a plain member
// who does not own it is still rejected. Guards against the group-manager
// relaxation leaking to ordinary agents.
func TestUpdateAgent_NormalAgentForbidsPlainMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "normal-config-"+uuid.NewString(), nil)
	memberID := createWorkspaceMemberUser(t, "normal-member", "normal-member-"+uuid.NewString()+"@example.com")

	req := newRequestAs(memberID, http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"max_concurrent_tasks": 3,
	})
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("plain member editing normal agent: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// Ordinary agents carry an empty managed_role in the API response — only
// group managers are stamped.
func TestGetAgent_ManagedRoleOnlyForGroupManager(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	normalID := createHandlerTestAgent(t, "plain-"+uuid.NewString(), nil)
	beckhamID := createHandlerTestAgent(t, "beckham-"+uuid.NewString(), nil)
	markGroupManagerForTest(t, beckhamID)

	for _, tc := range []struct {
		id   string
		want string
	}{{normalID, ""}, {beckhamID, "group_manager"}} {
		w := httptest.NewRecorder()
		testHandler.GetAgent(w, withURLParam(newRequest(http.MethodGet, "/api/agents/"+tc.id, nil), "id", tc.id))
		if w.Code != http.StatusOK {
			t.Fatalf("GetAgent %s: expected 200, got %d: %s", tc.id, w.Code, w.Body.String())
		}
		var resp AgentResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.ManagedRole != tc.want {
			t.Errorf("agent %s managed_role = %q, want %q", tc.id, resp.ManagedRole, tc.want)
		}
	}
}
