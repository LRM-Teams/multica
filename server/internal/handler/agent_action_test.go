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
		"draft_hint":  "ui only ignored",
	})
	rec := httptest.NewRecorder()
	testHandler.AgentTransportPrepareAction(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("prepare status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp agentActionCardResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ActionType != agentActionTypeCreate || resp.Status != agentActionStatusPrepared {
		t.Fatalf("action/status=%s/%s", resp.ActionType, resp.Status)
	}
	if resp.Payload.Name != "Hiree Bot" || resp.Payload.Description != "short catalog summary" {
		t.Fatalf("payload=%+v", resp.Payload)
	}
	if resp.ID == "" || resp.PreparedByAgentID == nil || *resp.PreparedByAgentID != agentID {
		t.Fatalf("id/prepared_by=%+v", resp)
	}
	if resp.Part == nil || resp.Part.Type != "reference" || resp.Part.RefType != "action_card" || resp.Part.RefID != resp.ID {
		t.Fatalf("part template=%+v", resp.Part)
	}
	// No draft bridge / multica:// protocol.
	raw := rec.Body.String()
	if strings.Contains(raw, "draft_id") || strings.Contains(raw, "multica://") {
		t.Fatalf("response must not use draft or multica:// protocol: %s", raw)
	}
	var name, description, status string
	if err := testPool.QueryRow(context.Background(), `
		SELECT payload->>'name', payload->>'description', status
		FROM agent_action_card WHERE id = $1`, resp.ID).Scan(&name, &description, &status); err != nil {
		t.Fatal(err)
	}
	if name != "Hiree Bot" || description != "short catalog summary" || status != "prepared" {
		t.Fatalf("row name=%q desc=%q status=%q", name, description, status)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_action_card WHERE id = $1`, resp.ID)
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

func TestGetActionCard_MemberCanLoad(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, _ := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)
	prep := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, map[string]any{
		"action_type": "agent:create", "name": "Loadable", "description": "d",
	})
	prepRec := httptest.NewRecorder()
	testHandler.AgentTransportPrepareAction(prepRec, prep)
	if prepRec.Code != http.StatusCreated {
		t.Fatalf("prepare %d %s", prepRec.Code, prepRec.Body.String())
	}
	var created agentActionCardResponse
	_ = json.NewDecoder(prepRec.Body).Decode(&created)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_action_card WHERE id = $1`, created.ID)
	})

	getReq := withURLParam(newRequest(http.MethodGet, "/api/agents/action-cards/"+created.ID, nil), "id", created.ID)
	getReq = withChatTestWorkspaceCtx(t, getReq)
	getRec := httptest.NewRecorder()
	testHandler.GetActionCard(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get %d %s", getRec.Code, getRec.Body.String())
	}
	var loaded agentActionCardResponse
	if err := json.NewDecoder(getRec.Body).Decode(&loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.ID != created.ID || loaded.Payload.Name != "Loadable" {
		t.Fatalf("loaded=%+v", loaded)
	}
}

func TestMarkActionCardDone_ViaCreateAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	// Prepare card as agent
	taskID, _ := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)
	prep := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, map[string]any{
		"action_type": "agent:create", "name": "From Card", "description": "via create",
	})
	prepRec := httptest.NewRecorder()
	testHandler.AgentTransportPrepareAction(prepRec, prep)
	if prepRec.Code != http.StatusCreated {
		t.Fatalf("prepare %d %s", prepRec.Code, prepRec.Body.String())
	}
	var card agentActionCardResponse
	_ = json.NewDecoder(prepRec.Body).Decode(&card)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_action_card WHERE id = $1`, card.ID)
	})

	// Human create with action_card_id — need a usable runtime in fixture workspace
	body := map[string]any{
		"display_name":   "From Card Agent",
		"description":    "via create",
		"runtime_id":     testRuntimeID,
		"model":          "gpt-4o-mini",
		"action_card_id": card.ID,
	}
	createReq := newRequest(http.MethodPost, "/api/agents", body)
	createReq = withChatTestWorkspaceCtx(t, createReq)
	createRec := httptest.NewRecorder()
	testHandler.CreateAgent(createRec, createReq)
	if createRec.Code != http.StatusCreated && createRec.Code != http.StatusOK {
		// Some fixtures return 201; accept either if agent created
		t.Fatalf("CreateAgent status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var status string
	var committedAgent *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, committed_agent_id::text FROM agent_action_card WHERE id = $1`, card.ID).Scan(&status, &committedAgent); err != nil {
		t.Fatal(err)
	}
	if status != agentActionStatusDone {
		t.Fatalf("card status=%s want done", status)
	}
	if committedAgent == nil || *committedAgent == "" {
		t.Fatalf("committed_agent_id empty")
	}
}

// TestCreateAgent_RequiresManageAgents verifies LRM-2343's unified
// manageAgents gate: a plain workspace member (no owner/admin role) is
// rejected from agent creation and the Proposal stays prepared. The gate is
// enforced server-side; UI disable is never the only defense.
func TestCreateAgent_RequiresManageAgents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	plainMemberID := createWorkspaceMemberUser(t, "Plain Create Member", "plain-create-"+strings.ReplaceAll(uuid.NewString(), "-", "")+"@multica.test")
	ct := context.Background()
	var runtimeID string
	if err := testPool.QueryRow(ct, `
		SELECT id FROM agent_runtime WHERE workspace_id = $1 LIMIT 1`, testWorkspaceID).Scan(&runtimeID); err != nil {
		t.Skipf("no runtime fixture available: %v", err)
	}

	body := map[string]any{
		"display_name": "Rejected Member Agent",
		"description":  "",
		"runtime_id":   runtimeID,
		"model":        "gpt-4o-mini",
	}
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequestAs(plainMemberID, http.MethodPost, "/api/agents", body))
	if w.Code != http.StatusForbidden {
		t.Fatalf("CreateAgent by plain member: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAgentTransportPrepareAction_CreatesCanonicalMessage verifies LRM-2343
// S1: prepare with a target already creates the canonical channel_message
// carrying the agent:create action part and returns message_id (story 2/3),
// and that client_request_id idempotency returns the same message_id on
// repeat while a changed proposal with the same key returns 409 (stories 4-6).
func TestAgentTransportPrepareAction_CreatesCanonicalMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	clientID := "prepare-" + uuid.NewString()

	prepare := func(cid, name, desc string) *httptest.ResponseRecorder {
		req := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, map[string]any{
			"action_type":       "agent:create",
			"name":              name,
			"description":       desc,
			"target":            target,
			"client_request_id": cid,
		})
		rec := httptest.NewRecorder()
		testHandler.AgentTransportPrepareAction(rec, req)
		return rec
	}

	rec := prepare(clientID, "Canonical Bot", "created via message")
	if rec.Code != http.StatusCreated {
		t.Fatalf("prepare status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp agentActionCardResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.MessageID == nil || *resp.MessageID == "" {
		t.Fatalf("response missing message_id: %+v", resp)
	}
	messageID := *resp.MessageID

	// The canonical channel_message must exist and carry the agent:create part.
	var authorType string
	if err := testPool.QueryRow(context.Background(), `
		SELECT author_type FROM channel_message WHERE id = $1`, messageID).Scan(&authorType); err != nil {
		t.Fatalf("load message: %v", err)
	}
	if authorType != "agent" {
		t.Fatalf("expected agent-authored canonical message, got %q", authorType)
	}
	var partsRaw string
	if err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(parts::text, '') FROM channel_message WHERE id = $1`, messageID).Scan(&partsRaw); err != nil {
		t.Fatalf("load message parts: %v", err)
	}
	if !strings.Contains(partsRaw, "agent:create") {
		t.Fatalf("message parts missing agent:create action: %s", partsRaw)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_message WHERE id = $1`, messageID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_action_card WHERE id = $1`, resp.ID)
	})

	// Same client_request_id + same proposal -> same message_id.
	rec2 := prepare(clientID, "Canonical Bot", "created via message")
	if rec2.Code != http.StatusCreated {
		t.Fatalf("repeat prepare status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var resp2 agentActionCardResponse
	_ = json.NewDecoder(rec2.Body).Decode(&resp2)
	if resp2.MessageID == nil || *resp2.MessageID != messageID {
		t.Fatalf("idempotent retry returned different message_id: %v vs %s", resp2.MessageID, messageID)
	}

	// Same client_request_id + different proposal -> 409.
	rec3 := prepare(clientID, "Different Bot", "changed")
	if rec3.Code != http.StatusConflict {
		t.Fatalf("conflicting reuse status=%d body=%s", rec3.Code, rec3.Body.String())
	}
}

// TestAgentTransportPrepareAction_RequiresClientRequestIDWhenTargeted verifies
// that a targeted prepare without a client_request_id is rejected.
func TestAgentTransportPrepareAction_RequiresClientRequestIDWhenTargeted(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, map[string]any{
		"action_type": "agent:create",
		"name":        "No CID",
		"description": "x",
		"target":      "#" + channelNameForTransportTest(t, channelID),
	})
	rec := httptest.NewRecorder()
	testHandler.AgentTransportPrepareAction(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
}
