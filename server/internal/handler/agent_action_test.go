package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentTransportPrepareAction_AgentCreateCard(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, _ := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)

	req := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, map[string]any{
		"action_type": "agent:create",
		"name":        "Hiree Bot",
		"description": "short catalog summary",
		"draft_hint":  "ui only",
	})
	rec := httptest.NewRecorder()
	testHandler.AgentTransportPrepareAction(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("prepare status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp agentActionPrepareResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ActionType != agentActionTypeCreate || resp.Status != agentActionStatusPrepared {
		t.Fatalf("action/status=%s/%s", resp.ActionType, resp.Status)
	}
	if resp.Payload.Name != "Hiree Bot" || resp.Payload.Description != "short catalog summary" {
		t.Fatalf("payload=%+v", resp.Payload)
	}
	if resp.DraftID == "" || resp.ID != resp.DraftID {
		t.Fatalf("id/draft_id mismatch: %+v", resp)
	}
	if !strings.Contains(resp.CardURL, "draft_id="+resp.DraftID) && !strings.Contains(resp.CardURL, "draft_id=") {
		t.Fatalf("card_url=%s", resp.CardURL)
	}
	if !strings.Contains(resp.Markdown, resp.CardURL) {
		t.Fatalf("markdown missing card_url: %s", resp.Markdown)
	}
	if resp.PreparedBy != agentID {
		t.Fatalf("prepared_by=%s want %s", resp.PreparedBy, agentID)
	}
	// Persisted draft is slim (no fat instructions path required for hire).
	var name, description, instructions, status string
	if err := testPool.QueryRow(t.Context(), `
		SELECT name, description, instructions, status
		FROM agent_creation_draft WHERE id = $1`, resp.DraftID).Scan(&name, &description, &instructions, &status); err != nil {
		t.Fatal(err)
	}
	if name != "Hiree Bot" || description != "short catalog summary" || instructions != "" || status != "draft" {
		t.Fatalf("row name=%q desc=%q instr=%q status=%q", name, description, instructions, status)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(t.Context(), `DELETE FROM agent_creation_draft WHERE id = $1`, resp.DraftID)
	})
}

func TestAgentTransportPrepareAction_RequiresNameAndSupportedType(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, _ := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)

	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"missing type", map[string]any{"name": "x"}, http.StatusBadRequest},
		{"bad type", map[string]any{"action_type": "channel:create", "name": "x"}, http.StatusBadRequest},
		{"missing name", map[string]any{"action_type": "agent:create"}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, tc.body)
			rec := httptest.NewRecorder()
			testHandler.AgentTransportPrepareAction(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestCreateAgentDraft_AgentActorGone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, _ := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)

	req := newRequest(http.MethodPost, "/api/agents/drafts", map[string]any{
		"name":        "should fail",
		"description": "agent path retired",
	})
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	req.Header.Set("X-Actor-Source", "task_token")
	rec := httptest.NewRecorder()
	testHandler.CreateAgentDraft(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("status=%d want 410 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "agent_draft_create_retired") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
