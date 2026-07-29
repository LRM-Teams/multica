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

func TestUpdateAgentWorkspaceRolePublishesCanonicalOwnerOnlyAgent(t *testing.T) {
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
	if len(got[0].RecipientUserIDs) != 1 || got[0].RecipientUserIDs[0] != testUserID {
		t.Fatalf("role-change recipients = %#v, want owner-only %s", got[0].RecipientUserIDs, testUserID)
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
