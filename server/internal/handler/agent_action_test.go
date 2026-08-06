package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAgentTransportPrepareAction_AtomicallyCreatesCanonicalProposal(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	clientID := "prepare-" + uuid.NewString()

	prepare := func(name, description string) *httptest.ResponseRecorder {
		req := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, map[string]any{
			"action_type":       "agent:create",
			"name":              name,
			"description":       description,
			"target":            target,
			"client_request_id": clientID,
		})
		rec := httptest.NewRecorder()
		testHandler.AgentTransportPrepareAction(rec, req)
		return rec
	}

	rec := prepare("Canonical Bot", "created via one message transaction")
	if rec.Code != http.StatusCreated {
		t.Fatalf("prepare status=%d body=%s", rec.Code, rec.Body.String())
	}
	var proposal agentActionProposalResponse
	if err := json.NewDecoder(rec.Body).Decode(&proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ActionType != agentActionTypeCreate || proposal.Status != agentActionStatusPrepared || proposal.MessageID == "" {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
	if proposal.Payload != (agentActionCreatePayload{Name: "Canonical Bot", Description: "created via one message transaction"}) || proposal.PreparedByAgentID != agentID {
		t.Fatalf("unexpected proposal payload: %+v", proposal)
	}
	if strings.Contains(rec.Body.String(), "action_card") || strings.Contains(rec.Body.String(), "runtime_id") {
		t.Fatalf("proposal response leaked retired or final config fields: %s", rec.Body.String())
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_message WHERE id = $1`, proposal.MessageID)
	})

	var actionCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_action WHERE channel_message_id = $1`, proposal.MessageID).Scan(&actionCount); err != nil {
		t.Fatalf("load canonical action: %v", err)
	}
	if actionCount != 1 {
		t.Fatalf("canonical action rows = %d, want 1", actionCount)
	}

	// The same key and payload reuses the same canonical Message and action.
	retry := prepare("Canonical Bot", "created via one message transaction")
	if retry.Code != http.StatusCreated {
		t.Fatalf("repeat prepare=%d body=%s", retry.Code, retry.Body.String())
	}
	var replay agentActionProposalResponse
	if err := json.NewDecoder(retry.Body).Decode(&replay); err != nil {
		t.Fatal(err)
	}
	if replay.MessageID != proposal.MessageID {
		t.Fatalf("replay message id=%q want %q", replay.MessageID, proposal.MessageID)
	}

	// Reusing an idempotency key with another proposal must never mutate the
	// canonical Message or create a second proposal record.
	conflict := prepare("Different Bot", "changed")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed replay=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestAgentTransportPrepareAction_RequiresCanonicalTargetAndClientID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing type", map[string]any{"name": "x", "target": target, "client_request_id": "a"}},
		{"bad type", map[string]any{"action_type": "channel:create", "name": "x", "target": target, "client_request_id": "a"}},
		{"missing name", map[string]any{"action_type": "agent:create", "target": target, "client_request_id": "a"}},
		{"missing target", map[string]any{"action_type": "agent:create", "name": "x", "client_request_id": "a"}},
		{"missing client id", map[string]any{"action_type": "agent:create", "name": "x", "target": target}},
		{"retired field", map[string]any{"action_type": "agent:create", "name": "x", "target": target, "client_request_id": "a", "channel_id": channelID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, tc.body)
			rec := httptest.NewRecorder()
			testHandler.AgentTransportPrepareAction(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCreateAgent_RequiresManageAgents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	plainMemberID := createWorkspaceMemberUser(t, "Plain Create Member", "plain-create-"+strings.ReplaceAll(uuid.NewString(), "-", "")+"@multica.test")
	ctx := context.Background()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent_runtime WHERE workspace_id = $1 LIMIT 1`, testWorkspaceID).Scan(&runtimeID); err != nil {
		t.Skipf("no runtime fixture available: %v", err)
	}
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequestAs(plainMemberID, http.MethodPost, "/api/agents", map[string]any{
		"display_name": "Rejected Member Agent",
		"runtime_id":   runtimeID,
		"model":        "gpt-4o-mini",
	}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("CreateAgent by plain member=%d body=%s", w.Code, w.Body.String())
	}

	// An AgentPrincipal is never allowed to fall through its owner's human
	// membership. The endpoint is a human-only boundary and must be 403 even
	// when the request still carries the owner identity stamp.
	agentReq := withAgentPrincipal(newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name": "Rejected Agent Principal",
		"runtime_id":   runtimeID,
		"model":        "gpt-4o-mini",
	}), uuid.NewString(), testWorkspaceID, testUserID)
	agentRec := httptest.NewRecorder()
	testHandler.CreateAgent(agentRec, agentReq)
	if agentRec.Code != http.StatusForbidden {
		t.Fatalf("CreateAgent by agent principal=%d body=%s", agentRec.Code, agentRec.Body.String())
	}
}

func TestCreateAgent_RejectsRetiredActionCardID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name":   "Retired Card Agent",
		"runtime_id":     testRuntimeID,
		"model":          "gpt-4o-mini",
		"action_card_id": uuid.NewString(),
	}))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "action_card_id has been retired") {
		t.Fatalf("retired card create=%d body=%s", w.Code, w.Body.String())
	}
}
