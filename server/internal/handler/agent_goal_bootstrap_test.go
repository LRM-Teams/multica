package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Task 16: the goal bootstrap binds channel→project through
// ChannelProjectBindingService inside its own transaction, so besides the
// 201 it must leave exactly one binding generation row — and the migration
// 470 guard proves no direct writer was used.
func TestAgentGoalBootstrapRecordsBindingGeneration(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	channel := createGoalTestChannel(t)
	managerID := createHandlerTestAgent(t, "Goal bootstrap manager "+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'manager')`,
		parseUUID(channel.ID), parseUUID(testWorkspaceID), parseUUID(managerID)); err != nil {
		t.Fatalf("add manager agent: %v", err)
	}

	created := httptest.NewRecorder()
	testHandler.CreateAgentChannelGoal(created, agentGoalRequest(
		t, managerID, http.MethodPost, "/api/agent/channels/"+channel.ID+"/goal", channel.ID,
		map[string]any{
			"title": "Bootstrap binding goal", "objective": "Coordinate delivery",
			"success_criteria": []string{"Control plane bound"},
		},
	))
	if created.Code != http.StatusCreated {
		t.Fatalf("manager create = %d: %s", created.Code, created.Body.String())
	}

	bootstrap := httptest.NewRecorder()
	testHandler.BootstrapAgentChannelGoalControlPlane(bootstrap, agentGoalRequest(
		t, managerID, http.MethodPost, "/api/agent/channels/"+channel.ID+"/goal/bootstrap", channel.ID,
		map[string]any{
			"project_title":  "Bootstrap binding project",
			"repository_url": "https://github.com/multica-ai/bootstrap-" + uuid.NewString(),
		},
	))
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("manager bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var resp BootstrapAgentChannelGoalControlPlaneResponse
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}

	var (
		generations int
		actor       string
	)
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*), COALESCE(max(actor), '')
		FROM graph_memory_channel_binding
		WHERE workspace_id = $1 AND channel_id = $2`,
		parseUUID(testWorkspaceID), parseUUID(channel.ID)).Scan(&generations, &actor); err != nil {
		t.Fatalf("load binding rows: %v", err)
	}
	if generations != 1 {
		t.Fatalf("binding generations = %d, want exactly 1 (single service-mediated bind)", generations)
	}
	if actor != "agent:"+managerID {
		t.Fatalf("binding actor = %q, want the bootstrap agent", actor)
	}

	// The channel really points at the bootstrapped project.
	var projectID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT project_id::text FROM channel WHERE id = $1`, parseUUID(channel.ID)).Scan(&projectID); err != nil {
		t.Fatalf("load channel project: %v", err)
	}
	if projectID != resp.Project.ID {
		t.Fatalf("channel project = %s, want bootstrapped %s", projectID, resp.Project.ID)
	}

	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, parseUUID(resp.Project.ID))
	})
}
