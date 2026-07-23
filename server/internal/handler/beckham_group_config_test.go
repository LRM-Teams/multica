package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// markGroupManagerForTest flags an agent as a per-group Beckham with
// visibility=channel bound to a home group (LRM-370).
func markGroupManagerForTest(t *testing.T, agentID string) {
	t.Helper()
	channelID := seedChannelForTest(t, "beckham-home-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET managed_role = 'group_manager', visibility = 'channel', home_channel_id = $2 WHERE id = $1`,
		agentID, channelID,
	); err != nil {
		t.Fatalf("mark group manager: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE channel SET group_manager_agent_id = $2 WHERE id = $1`, channelID, agentID,
	); err != nil {
		t.Fatalf("bind group manager channel: %v", err)
	}
}

// A group-manager agent (Beckham) is shared infrastructure: any workspace
// member can tune its five runtime properties, while the API keeps all other
// agent management operations owner/admin-only.
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

// Keep the side-panel payload contract explicit: a plain workspace member may
// send exactly the five runtime properties. model_catalog_request_id is the
// transient proof carried by the later #559 discovery flow, not a privilege
// expansion. This exercises the field gate separately from provider/catalog
// validation, which is covered by the agent update contract tests.
func TestCanUpdateAgent_GroupManagerAllowsRuntimePayloadFields(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "beckham-payload-"+uuid.NewString(), nil)
	markGroupManagerForTest(t, agentID)
	memberID := createWorkspaceMemberUser(t, "payload-member", "payload-member-"+uuid.NewString()+"@example.com")
	agent, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}

	rawFields := map[string]json.RawMessage{
		"runtime_id":               json.RawMessage(`"runtime-id"`),
		"model":                    json.RawMessage(`"model-id"`),
		"model_catalog_request_id": json.RawMessage(`"catalog-proof"`),
		"thinking_level":           json.RawMessage(`"high"`),
		"max_concurrent_tasks":     json.RawMessage(`3`),
	}
	w := httptest.NewRecorder()
	if !testHandler.canUpdateAgent(w, newRequestAs(memberID, http.MethodPut, "/api/agents/"+agentID, nil), agent, rawFields) {
		t.Fatalf("group-manager runtime payload unexpectedly rejected: %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateAgent_GroupManagerForbidsPlainMemberIdentityChanges(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "beckham-identity-"+uuid.NewString(), nil)
	markGroupManagerForTest(t, agentID)
	memberID := createWorkspaceMemberUser(t, "identity-member", "identity-member-"+uuid.NewString()+"@example.com")

	for _, body := range []map[string]any{
		{"description": "member must not be able to edit this"},
		{"instructions": "member must not be able to edit this"},
		{"custom_args": []string{"--unsafe"}},
	} {
		req := newRequestAs(memberID, http.MethodPut, "/api/agents/"+agentID, body)
		req = withURLParam(req, "id", agentID)
		w := httptest.NewRecorder()
		testHandler.UpdateAgent(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("plain member updating %v: expected 403, got %d: %s", body, w.Code, w.Body.String())
		}
	}
}

func TestArchiveAgent_GroupManagerForbidsPlainMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "beckham-archive-"+uuid.NewString(), nil)
	markGroupManagerForTest(t, agentID)
	memberID := createWorkspaceMemberUser(t, "archive-member", "archive-member-"+uuid.NewString()+"@example.com")

	req := newRequestAs(memberID, http.MethodPost, "/api/agents/"+agentID+"/archive", nil)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.ArchiveAgent(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("plain member archiving group manager: expected 403, got %d: %s", w.Code, w.Body.String())
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
