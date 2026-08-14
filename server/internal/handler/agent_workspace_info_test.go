package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
)

func TestGetAgentWorkspaceInfoOrdinaryAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}

	const secretInstructions = "WORKSPACE_INFO_MUST_NOT_LEAK_INSTRUCTIONS"
	agentID := createHandlerTestAgent(t, "WorkspaceInfoMember", []byte("[]"))
	privateSiblingRuntimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent
		SET workspace_role = 'member', instructions = $2
		WHERE id = $1`, parseUUID(agentID), secretInstructions); err != nil {
		t.Fatalf("set ordinary agent role: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent/workspace-info", nil)
	req = req.WithContext(middleware.WithAgentPrincipal(req.Context(), middleware.AgentPrincipal{
		AgentID:     agentID,
		WorkspaceID: testWorkspaceID,
		OwnerUserID: testUserID,
		ActorSource: "agent_credential",
	}))
	rec := httptest.NewRecorder()
	testHandler.GetAgentWorkspaceInfo(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace info status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secretInstructions) {
		t.Fatalf("workspace info leaked agent instructions: %s", rec.Body.String())
	}

	var payload struct {
		Workspace WorkspaceResponse            `json:"workspace"`
		Agents    []map[string]json.RawMessage `json:"agents"`
		Computers []map[string]json.RawMessage `json:"computers"`
		Tasks     []map[string]json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode workspace info: %v body=%s", err, rec.Body.String())
	}
	if payload.Workspace.ID != testWorkspaceID {
		t.Fatalf("workspace id=%q want %q", payload.Workspace.ID, testWorkspaceID)
	}
	if payload.Agents == nil || payload.Computers == nil || payload.Tasks == nil {
		t.Fatalf("workspace info arrays must be present, got agents=%v computers=%v tasks=%v", payload.Agents, payload.Computers, payload.Tasks)
	}

	foundCaller := false
	for _, item := range payload.Agents {
		for _, forbidden := range []string{"instructions", "runtime_config", "mcp_config", "owner_id", "custom_env"} {
			if _, leaked := item[forbidden]; leaked {
				t.Fatalf("agent workspace info leaked %q: %v", forbidden, item)
			}
		}
		var id string
		if err := json.Unmarshal(item["id"], &id); err == nil && id == agentID {
			foundCaller = true
			if _, ok := item["status"]; !ok {
				t.Fatalf("caller agent is missing status: %v", item)
			}
		}
	}
	if !foundCaller {
		t.Fatalf("ordinary caller agent %s missing from workspace info", agentID)
	}
	for _, item := range payload.Computers {
		for _, forbidden := range []string{"owner_id", "metadata", "capabilities", "launch_header"} {
			if _, leaked := item[forbidden]; leaked {
				t.Fatalf("computer workspace info leaked %q: %v", forbidden, item)
			}
		}
		var id string
		if err := json.Unmarshal(item["id"], &id); err == nil && id == privateSiblingRuntimeID {
			t.Fatalf("agent borrowed OwnerUserID visibility for unbound private Computer %s", id)
		}
	}
}
