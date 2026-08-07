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

func createWindyDraftTestMember(t *testing.T, label string) string {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	name := label + " " + suffix
	email := label + "-" + suffix + "@multica.test"

	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, name, email).Scan(&userID); err != nil {
		t.Fatalf("create %s user: %v", label, err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, userID); err != nil {
		t.Fatalf("add %s as member: %v", label, err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func TestCreateAgentDraft_TaskTokenRetiredForAgents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	initiatorID := createWindyDraftTestMember(t, "Wendy Draft Initiator")
	_ = initiatorID
	wendyID := createHandlerTestAgent(t, "wendy_draft_target_"+strings.ReplaceAll(uuid.NewString(), "-", "_"), nil)
	taskID := createHandlerTestTaskForAgent(t, wendyID)

	req := newRequest(http.MethodPost, "/api/agents/drafts", map[string]any{
		"name":         "Conversation Researcher",
		"description":  "Turns the current discussion into research notes.",
		"instructions": "Help the person who asked Wendy.",
		"visibility":   "private",
	})
	req.Header.Set("X-Agent-ID", wendyID)
	req.Header.Set("X-Task-ID", taskID)
	req.Header.Set("X-Actor-Source", "task_token")

	w := httptest.NewRecorder()
	testHandler.CreateAgentDraft(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("CreateAgentDraft with task token: expected 410, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAgentPrepareAction_RequiresCanonicalProposalTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, _ := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	agentID := agentIDForTask(t, taskID)
	bindOnboardingAgentForTest(t, agentID)
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, map[string]any{
		"action_type": "agent:create",
		"name":        "Targeted Hire",
		"description": "owner-targeted",
	})
	w := httptest.NewRecorder()
	testHandler.AgentTransportPrepareAction(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("prepare without target expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateAgentDraft_AgentCredentialRetiredForAgents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	initiatorID := createWindyDraftTestMember(t, "Wendy Inbox Initiator")
	wendyID := createHandlerTestAgent(t, "wendy_inbox_draft_target_"+strings.ReplaceAll(uuid.NewString(), "-", "_"), nil)
	ctx := context.Background()

	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, testWorkspaceID, "wendy-draft-"+strings.ReplaceAll(uuid.NewString(), "-", ""), initiatorID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	var messageID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content)
		VALUES ($1, $2, 'user', $3, 'Wendy Inbox Initiator', 'please hire this agent')
		RETURNING id
	`, channelID, testWorkspaceID, initiatorID).Scan(&messageID); err != nil {
		t.Fatalf("create channel message: %v", err)
	}

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (workspace_id, channel_id, agent_id, source_message_id, reason, status, priority)
		VALUES ($1, $2, $3, $4, 'mention', 'pending', 10)
		RETURNING id
	`, testWorkspaceID, channelID, wendyID, messageID).Scan(&eventID); err != nil {
		t.Fatalf("create inbox event: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/agents/drafts", map[string]any{
		"name":         "Inbox Researcher",
		"description":  "Generated from an inbox-delivered Wendy request.",
		"instructions": "Help the member who mentioned Wendy.",
		"visibility":   "private",
	})
	req.Header.Set("X-Agent-ID", wendyID)
	req.Header.Set("X-Agent-Inbox-Event-ID", eventID)
	req.Header.Set("X-Actor-Source", "agent_credential")

	w := httptest.NewRecorder()
	testHandler.CreateAgentDraft(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("CreateAgentDraft with agent credential: expected 410, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetAgentDraft_AllowsWorkspaceMemberViewer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	viewerID := createWindyDraftTestMember(t, "Wendy Draft Viewer")

	createReq := newRequest(http.MethodPost, "/api/agents/drafts", map[string]any{
		"name":         "Hiring Card Viewer",
		"description":  "Visible to everyone in the workspace.",
		"instructions": "Review the Wendy hiring card.",
		"visibility":   "private",
	})
	createRec := httptest.NewRecorder()
	testHandler.CreateAgentDraft(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateAgentDraft setup: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created AgentCreationDraftResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created draft: %v", err)
	}
	if created.TargetUserID == viewerID {
		t.Fatalf("test setup needs a non-target viewer")
	}

	getReq := withURLParam(newRequestAs(viewerID, http.MethodGet, "/api/agents/drafts/"+created.ID, nil), "draftId", created.ID)
	getRec := httptest.NewRecorder()
	testHandler.GetAgentDraft(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetAgentDraft as workspace non-target: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}

	var loaded AgentCreationDraftResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &loaded); err != nil {
		t.Fatalf("decode loaded draft: %v", err)
	}
	if loaded.ID != created.ID || loaded.TargetUserID != created.TargetUserID {
		t.Fatalf("loaded draft = {id:%q target:%q}, want {id:%q target:%q}", loaded.ID, loaded.TargetUserID, created.ID, created.TargetUserID)
	}
}

func TestCreateAgent_FromDraftAllowsNonTargetWorkspaceMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// LRM-444: GetAgentDraft is workspace-visible, but Create used to require
	// target_user_id == creator. Hiring cards posted in channels then fail
	// with "agent draft not found" for the member who clicks Create.
	// Create as the fixture owner (owns the shared test runtime); draft is
	// stamped for a different target.
	targetID := createWindyDraftTestMember(t, "Wendy Draft Target")
	if targetID == testUserID {
		t.Fatal("test setup needs a non-fixture target")
	}

	marker := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	var draftID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_creation_draft (
			workspace_id, target_user_id, name, description, instructions,
			initial_notes, initial_memory, status
		) VALUES (
			$1, $2, $3, 'hiring card', 'help the requester',
			'{"notes/context.md":"from-draft"}'::jsonb, '{}'::jsonb, 'draft'
		)
		RETURNING id
	`, testWorkspaceID, targetID, "Mira "+marker).Scan(&draftID); err != nil {
		t.Fatalf("insert draft: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_creation_draft WHERE id = $1`, draftID)
	})

	req := newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":                 "from-draft-" + marker,
		"display_name":         "from-draft-" + marker,
		"runtime_id":           handlerTestRuntimeID(t),
		"model":                "composer-1.5",
		"visibility":           "private",
		"max_concurrent_tasks": 1,
		"draft_id":             draftID,
	})
	rec := httptest.NewRecorder()
	testHandler.CreateAgent(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateAgent as non-target member: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created AgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.ID)
	})
	if created.OwnerID == nil || *created.OwnerID != testUserID {
		t.Fatalf("created owner = %v, want fixture user %q", created.OwnerID, testUserID)
	}

	var status string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status FROM agent_creation_draft WHERE id = $1
	`, draftID).Scan(&status); err != nil {
		t.Fatalf("reload draft status: %v", err)
	}
	if status != "used" {
		t.Fatalf("draft status = %q, want used", status)
	}
}

func TestCreateAgent_FromDraftAlreadyUsedReturnsConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	marker := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	var draftID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_creation_draft (
			workspace_id, target_user_id, name, status
		) VALUES ($1, $2, $3, 'used')
		RETURNING id
	`, testWorkspaceID, testUserID, "used-draft-"+marker).Scan(&draftID); err != nil {
		t.Fatalf("insert used draft: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_creation_draft WHERE id = $1`, draftID)
	})

	req := newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":                 "reuse-draft-" + marker,
		"display_name":         "reuse-draft-" + marker,
		"runtime_id":           handlerTestRuntimeID(t),
		"model":                "composer-1.5",
		"visibility":           "private",
		"max_concurrent_tasks": 1,
		"draft_id":             draftID,
	})
	rec := httptest.NewRecorder()
	testHandler.CreateAgent(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("CreateAgent with used draft: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["code"] != agentDraftLookupAlreadyUsed {
		t.Fatalf("error code = %q, want %q", body["code"], agentDraftLookupAlreadyUsed)
	}
}
